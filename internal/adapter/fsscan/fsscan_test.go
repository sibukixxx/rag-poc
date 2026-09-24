package fsscan_test

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/sibukixxx/rag-poc/internal/adapter/fsscan"
)

func supportedDocs(p string) bool {
	switch path.Ext(p) {
	case ".md", ".txt", ".pdf", ".html", ".csv", ".json":
		return true
	}
	return false
}

func scanAll(t *testing.T, fsys fs.FS, rules fsscan.Rules) ([]string, fsscan.Summary) {
	t.Helper()
	var got []string
	summary, err := fsscan.Scan(fsys, rules, supportedDocs, func(e fsscan.Entry) error {
		got = append(got, e.Path)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return got, summary
}

func TestScanWalksRecursivelyAndSkipsSecretsUnsupportedAndSymlinks(t *testing.T) {
	fsys := fstest.MapFS{
		"README.md":                 {Data: []byte("root")},
		"docs/guide.md":             {Data: []byte("guide")},
		"docs/deep/nested/spec.pdf": {Data: []byte("%PDF")},
		"docs/image.png":            {Data: []byte("png")},
		".env":                      {Data: []byte("SECRET=1")},
		"config/.env.production":    {Data: []byte("SECRET=2")},
		"keys/id_rsa":               {Data: []byte("key")},
		"certs/server.pem":          {Data: []byte("pem")},
		".git/HEAD.md":              {Data: []byte("ref")},
		"aws/credentials.json":      {Data: []byte("{}")},
		"link.md":                   {Data: []byte("docs/guide.md"), Mode: fs.ModeSymlink},
	}

	got, summary := scanAll(t, fsys, fsscan.Rules{})

	if want := []string{"README.md", "docs/deep/nested/spec.pdf", "docs/guide.md"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("scanned = %v, want %v", got, want)
	}
	want := fsscan.Summary{Files: 3, ExcludedSensitive: 6, Unsupported: 1, Symlinks: 1}
	if summary != want {
		t.Fatalf("summary = %+v, want %+v", summary, want)
	}
}

func TestScanAppliesOperatorIncludeAndExcludePatterns(t *testing.T) {
	fsys := fstest.MapFS{
		"handbook/policy.md":   {Data: []byte("a")},
		"handbook/drafts/x.md": {Data: []byte("b")},
		"handbook/notes.txt":   {Data: []byte("c")},
		"archive/old.md":       {Data: []byte("d")},
	}

	got, summary := scanAll(t, fsys, fsscan.Rules{Include: []string{"handbook/"}, Exclude: []string{"handbook/drafts/", "*.txt"}})

	if want := []string{"handbook/policy.md"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("scanned = %v, want %v", got, want)
	}
	if summary.ExcludedByRule != 3 {
		t.Fatalf("excluded by rule = %d, want 3 (drafts, txt, outside include)", summary.ExcludedByRule)
	}
}

// countingFS fails the test if discovery opens a regular file: enumeration
// must work from directory metadata alone so huge corpora are not read.
type countingFS struct {
	fstest.MapFS
	fileOpens atomic.Int64
}

func (c *countingFS) Open(name string) (fs.File, error) {
	f, err := c.MapFS.Open(name)
	if err == nil {
		if st, statErr := f.Stat(); statErr == nil && !st.IsDir() {
			c.fileOpens.Add(1)
		}
	}
	return f, err
}

func TestScanEnumeratesTenThousandFilesWithoutReadingContents(t *testing.T) {
	m := fstest.MapFS{}
	for i := 0; i < 10_000; i++ {
		m[fmt.Sprintf("corpus/d%03d/file%05d.md", i%100, i)] = &fstest.MapFile{Data: []byte("x")}
	}
	fsys := &countingFS{MapFS: m}

	count := 0
	summary, err := fsscan.Scan(fsys, fsscan.Rules{}, supportedDocs, func(e fsscan.Entry) error {
		count++
		if e.Size != 1 {
			return fmt.Errorf("%s size = %d, want 1 from metadata", e.Path, e.Size)
		}
		return nil
	})

	if err != nil {
		t.Fatal(err)
	}
	if count != 10_000 || summary.Files != 10_000 {
		t.Fatalf("enumerated %d files (summary %d), want 10000", count, summary.Files)
	}
	if n := fsys.fileOpens.Load(); n != 0 {
		t.Fatalf("discovery opened %d regular files, want 0", n)
	}
}

func TestValidateRulesRejectsMalformedPatterns(t *testing.T) {
	err := fsscan.Rules{Exclude: []string{"[unclosed"}}.Validate()

	if err == nil || err.Error() != `exclude pattern "[unclosed": syntax error in pattern` {
		t.Fatalf("error = %v", err)
	}
}

func TestScanOnDiskNeverFollowsSymlinksOutOfTheRoot(t *testing.T) {
	base := t.TempDir()
	root, outside := filepath.Join(base, "root"), filepath.Join(base, "outside")
	for _, dir := range []string{root, outside} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "doc.md"), []byte("inside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.md"), []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape-dir")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.md"), filepath.Join(root, "escape.md")); err != nil {
		t.Fatal(err)
	}

	got, summary := scanAll(t, os.DirFS(root), fsscan.Rules{})

	if want := []string{"doc.md"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("scanned = %v, want %v", got, want)
	}
	if summary.Symlinks != 2 {
		t.Fatalf("symlinks = %d, want 2", summary.Symlinks)
	}
}
