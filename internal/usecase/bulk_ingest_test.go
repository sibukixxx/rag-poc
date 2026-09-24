package usecase_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sibukixxx/rag-poc/internal/adapter/extractor"
	"github.com/sibukixxx/rag-poc/internal/adapter/fsscan"
	"github.com/sibukixxx/rag-poc/internal/adapter/sqlite"
	"github.com/sibukixxx/rag-poc/internal/adapter/tokenizer"
	"github.com/sibukixxx/rag-poc/internal/domain/bulk"
	"github.com/sibukixxx/rag-poc/internal/domain/knowledge"
	"github.com/sibukixxx/rag-poc/internal/usecase"
)

// hookedIngester wraps the real IngestUseCase so tests can inject a
// transient failure or act (pause, cancel) after a given number of files.
type hookedIngester struct {
	next      usecase.FileIngester
	mu        sync.Mutex
	calls     int
	failFirst map[string]int // path -> remaining injected failures
	afterCall func(n int)
}

func (h *hookedIngester) IngestFile(ctx context.Context, kbID, filename, mime string, data []byte) (*knowledge.Document, error) {
	h.mu.Lock()
	h.calls++
	n := h.calls
	inject := h.failFirst[filename] > 0
	if inject {
		h.failFirst[filename]--
	}
	h.mu.Unlock()
	if inject {
		return nil, errors.New("embedding chunks: provider returned HTTP 503")
	}
	doc, err := h.next.IngestFile(ctx, kbID, filename, mime, data)
	if h.afterCall != nil {
		h.afterCall(n)
	}
	return doc, err
}

func (h *hookedIngester) callCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.calls
}

type bulkFixture struct {
	db       *sql.DB
	uc       *usecase.BulkIngestUseCase
	ingester *hookedIngester
	embedder *countingEmbedder
	kbID     string
	root     string
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newBulkIngestFixture(t *testing.T) bulkFixture {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "share")
	writeFile(t, root, "handbook/refunds.md", "# Refunds\n\nRefunds are accepted within 30 days of purchase.")
	writeFile(t, root, "handbook/shipping/osaka.md", "# Shipping\n\nOrders ship from the Osaka warehouse.")
	writeFile(t, root, "faq.txt", "Support hours are 9 to 18 JST.")
	writeFile(t, root, ".env", "API_KEY=do-not-ingest")

	db, err := sqlite.Open(filepath.Join(base, "forgeai.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ks := sqlite.NewKnowledgeStore(db)
	kb, err := ks.EnsureKnowledgeBase(context.Background(), "Share", "share")
	if err != nil {
		t.Fatal(err)
	}
	tok, err := tokenizer.New()
	if err != nil {
		t.Fatal(err)
	}
	embedder := &countingEmbedder{}
	registry := extractor.NewDefaultRegistry()
	ingest := usecase.NewIngestUseCase(ks, registry, tok, embedder, testPrices(), sqlite.NewTraceStore(db))
	hooked := &hookedIngester{next: ingest, failFirst: map[string]int{}}
	supported := func(p string) bool { _, ok := registry.Find(path.Base(p), ""); return ok }

	uc := usecase.NewBulkIngestUseCase(sqlite.NewBulkStore(db), sqlite.NewSourceStore(db), hooked, fsscan.LocalCorpus{Supported: supported})
	uc.AllowedRoots = []string{base}
	uc.Workers = 3
	uc.RetryDelay = 0
	return bulkFixture{db: db, uc: uc, ingester: hooked, embedder: embedder, kbID: kb.ID, root: root}
}

func (f bulkFixture) connect(t *testing.T) string {
	t.Helper()
	conn, err := f.uc.CreateFilesystemConnection(context.Background(), f.kbID, "share", usecase.FilesystemSourceConfig{Root: f.root})
	if err != nil {
		t.Fatal(err)
	}
	return conn.ID
}

func (f bulkFixture) runNewJob(t *testing.T, connID string) usecase.JobProgress {
	t.Helper()
	ctx := context.Background()
	job, err := f.uc.StartJob(ctx, connID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.uc.Run(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	p, err := f.uc.Progress(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func (f bulkFixture) searchable(t *testing.T, query string) []string {
	t.Helper()
	results, err := sqlite.NewFTSStore(f.db).Search(context.Background(), f.kbID, query, 10)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range results {
		names = append(names, r.Filename)
	}
	return names
}

func countTable(t *testing.T, db *sql.DB, query string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestBulkIngestRunIndexesNestedFilesAndSkipsSecrets(t *testing.T) {
	f := newBulkIngestFixture(t)

	p := f.runNewJob(t, f.connect(t))

	if p.Job.Status != bulk.JobCompleted {
		t.Fatalf("job = %+v", p.Job)
	}
	if want := (bulk.Counts{Discovered: 3, Completed: 3}); p.Counts != want {
		t.Fatalf("counts = %+v, want %+v", p.Counts, want)
	}
	if p.Job.Scan.ExcludedSensitive != 1 {
		t.Fatalf("scan = %+v, want the .env file excluded", p.Job.Scan)
	}
	if got := f.searchable(t, "Osaka warehouse"); len(got) != 1 || got[0] != "handbook/shipping/osaka.md" {
		t.Fatalf("search = %v, want the nested file cited by its relative path", got)
	}
	if got := f.searchable(t, "do-not-ingest"); len(got) != 0 {
		t.Fatalf(".env content is searchable: %v", got)
	}
}

func TestBulkIngestRerunSkipsUnchangedFilesWithoutReembedding(t *testing.T) {
	f := newBulkIngestFixture(t)
	connID := f.connect(t)
	f.runNewJob(t, connID)
	embedCalls := f.embedder.calls

	p := f.runNewJob(t, connID)

	if want := (bulk.Counts{Discovered: 3, Skipped: 3}); p.Counts != want {
		t.Fatalf("counts = %+v, want %+v", p.Counts, want)
	}
	if f.embedder.calls != embedCalls {
		t.Fatalf("embedder called %d more times for unchanged files", f.embedder.calls-embedCalls)
	}
}

func TestBulkIngestReconcilesUpdatedAndRemovedFiles(t *testing.T) {
	f := newBulkIngestFixture(t)
	connID := f.connect(t)
	f.runNewJob(t, connID)
	writeFile(t, f.root, "handbook/refunds.md", "# Refunds\n\nRefunds are now accepted within 60 days.")
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(f.root, "handbook/refunds.md"), future, future); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(f.root, "faq.txt")); err != nil {
		t.Fatal(err)
	}

	p := f.runNewJob(t, connID)

	if p.Job.Deleted != 1 || p.Counts.Completed != 1 || p.Counts.Skipped != 1 {
		t.Fatalf("progress = %+v / %+v, want 1 updated, 1 skipped, 1 deleted", p.Job, p.Counts)
	}
	if got := f.searchable(t, "60 days"); len(got) != 1 {
		t.Fatalf("updated text not searchable: %v", got)
	}
	if got := f.searchable(t, "30 days"); len(got) != 0 {
		t.Fatalf("old version still searchable: %v", got)
	}
	if got := f.searchable(t, "Support hours"); len(got) != 0 {
		t.Fatalf("removed file still searchable: %v", got)
	}
	if n := countTable(t, f.db, `SELECT COUNT(1) FROM documents`); n != 2 {
		t.Fatalf("documents = %d, want 2 (old versions and removed files purged)", n)
	}
}

func TestBulkIngestMalformedFileFailsAloneAndTransientErrorsAreRetried(t *testing.T) {
	f := newBulkIngestFixture(t)
	writeFile(t, f.root, "broken.pdf", "this is not a pdf")
	f.ingester.failFirst["faq.txt"] = 1

	p := f.runNewJob(t, f.connect(t))

	if p.Job.Status != bulk.JobCompleted || p.Counts.Completed != 3 || p.Counts.Failed != 1 {
		t.Fatalf("progress = %+v / %+v, want 3 completed and 1 failed", p.Job, p.Counts)
	}
	if len(p.Failures) != 1 || p.Failures[0].Path != "broken.pdf" || p.Failures[0].Attempts != 1 {
		t.Fatalf("failures = %+v, want broken.pdf failed once (not retried)", p.Failures)
	}
	if p.Job.LastError != "1 file(s) failed" {
		t.Fatalf("last error = %q", p.Job.LastError)
	}
	if n := countTable(t, f.db, `SELECT COUNT(1) FROM documents WHERE status = 'failed'`); n != 0 {
		t.Fatalf("%d failed document rows left behind", n)
	}
}

func TestBulkIngestInterruptedRunResumesWithoutDuplicates(t *testing.T) {
	f := newBulkIngestFixture(t)
	f.uc.Workers = 1
	connID := f.connect(t)
	ctx, cancel := context.WithCancel(context.Background())
	f.ingester.afterCall = func(n int) {
		if n == 1 {
			cancel() // the process "dies" after the first file
		}
	}
	job, err := f.uc.StartJob(ctx, connID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.uc.Run(ctx, job.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted run error = %v, want context.Canceled", err)
	}
	f.ingester.afterCall = nil

	resumed, err := f.uc.ResumeUnfinished(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	p, _ := f.uc.Progress(context.Background(), job.ID)
	if resumed != 1 || p.Job.Status != bulk.JobCompleted || p.Counts.Completed != 3 {
		t.Fatalf("resumed=%d progress = %+v / %+v", resumed, p.Job, p.Counts)
	}
	if f.ingester.callCount() != 3 {
		t.Fatalf("ingester called %d times, want 3 (no file processed twice)", f.ingester.callCount())
	}
	if n := countTable(t, f.db, `SELECT COUNT(1) FROM source_items`); n != 3 {
		t.Fatalf("source items = %d, want 3", n)
	}
}

func TestBulkIngestPauseStopsBetweenBatchesAndResumeFinishes(t *testing.T) {
	f := newBulkIngestFixture(t)
	f.uc.Workers = 1
	connID := f.connect(t)
	job, err := f.uc.StartJob(context.Background(), connID)
	if err != nil {
		t.Fatal(err)
	}
	f.ingester.afterCall = func(n int) {
		if n == 1 {
			if err := f.uc.Pause(context.Background(), job.ID); err != nil {
				t.Error(err)
			}
		}
	}

	if _, err := f.uc.Run(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	paused, _ := f.uc.Progress(context.Background(), job.ID)
	f.ingester.afterCall = nil
	if err := f.uc.Resume(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.uc.Run(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	done, _ := f.uc.Progress(context.Background(), job.ID)

	if paused.Job.Status != bulk.JobPaused || paused.Counts.Pending == 0 {
		t.Fatalf("after pause = %+v / %+v, want paused with pending files", paused.Job, paused.Counts)
	}
	if done.Job.Status != bulk.JobCompleted || done.Counts.Completed != 3 {
		t.Fatalf("after resume = %+v / %+v", done.Job, done.Counts)
	}
}

func TestBulkIngestCreateConnectionRejectsRootsOutsideTheAllowlist(t *testing.T) {
	f := newBulkIngestFixture(t)
	outside := t.TempDir()
	link := filepath.Join(f.root, "..", "link-out")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	for _, root := range []string{outside, link, "/etc", "relative/path"} {
		_, err := f.uc.CreateFilesystemConnection(context.Background(), f.kbID, "x", usecase.FilesystemSourceConfig{Root: root})
		if err == nil || !strings.Contains(err.Error(), "not under an allowed root") && !strings.Contains(err.Error(), "must be absolute") {
			t.Errorf("root %s: error = %v, want refusal", root, err)
		}
	}
	f.uc.AllowedRoots = nil
	_, err := f.uc.CreateFilesystemConnection(context.Background(), f.kbID, "x", usecase.FilesystemSourceConfig{Root: f.root})
	if err == nil || err.Error() != "filesystem sources are disabled: set sources.filesystem.allowed_roots" {
		t.Fatalf("error without allowlist = %v", err)
	}
}
