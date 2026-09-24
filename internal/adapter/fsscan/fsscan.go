// Package fsscan enumerates a filesystem corpus for bulk ingestion (#30).
// It streams entries from directory metadata only — file contents are never
// read here — so a corpus of hundreds of thousands of files can be listed
// in bounded memory. Paths are relative, slash-separated, and confined to
// the fs.FS root, so a scan cannot escape the configured directory.
package fsscan

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/sibukixxx/rag-poc/internal/domain/bulk"
)

// Rules is bulk.Rules; the alias keeps call sites short.
type Rules = bulk.Rules

// Entry and Summary are the domain types produced by a scan.
type (
	Entry   = bulk.Entry
	Summary = bulk.ScanSummary
)

// ValidateRules reports malformed patterns up front instead of mid-scan.
func ValidateRules(r Rules) error {
	for kind, patterns := range map[string][]string{"include": r.Include, "exclude": r.Exclude} {
		for _, p := range patterns {
			if strings.HasSuffix(p, "/") {
				continue
			}
			if _, err := path.Match(p, ""); err != nil {
				return fmt.Errorf("%s pattern %q: %w", kind, p, err)
			}
		}
	}
	return nil
}

// Paths that commonly hold credentials are never ingested, even when an
// operator's include pattern would match them.
var (
	sensitiveDirs  = map[string]bool{".git": true, ".ssh": true, ".aws": true, ".gnupg": true, ".kube": true, ".docker": true}
	sensitiveNames = []string{
		".env", ".env.*", "*.pem", "*.key", "*.p12", "*.pfx", "*.kdbx",
		"id_rsa*", "id_dsa*", "id_ecdsa*", "id_ed25519*",
		"credentials*", ".netrc", ".npmrc", ".pgpass",
	}
)

func isSensitiveName(name string) bool {
	for _, p := range sensitiveNames {
		if ok, _ := path.Match(p, name); ok {
			return true
		}
	}
	return false
}

func matches(pattern, rel string) bool {
	if strings.HasSuffix(pattern, "/") {
		return strings.HasPrefix(rel+"/", pattern) || strings.HasPrefix(rel, pattern)
	}
	if ok, _ := path.Match(pattern, rel); ok {
		return true
	}
	ok, _ := path.Match(pattern, path.Base(rel))
	return ok
}

func anyMatch(patterns []string, rel string) bool {
	for _, p := range patterns {
		if matches(p, rel) {
			return true
		}
	}
	return false
}

// Scan walks fsys in lexical order and calls fn for each regular file that
// is supported, not sensitive, and allowed by rules. Symbolic links are
// never followed. Returning an error from fn stops the scan.
func Scan(fsys fs.FS, rules Rules, supported func(path string) bool, fn func(Entry) error) (Summary, error) {
	if err := ValidateRules(rules); err != nil {
		return Summary{}, err
	}
	var s Summary
	err := fs.WalkDir(fsys, ".", func(rel string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if rel == "." {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			s.Symlinks++
			return nil
		}
		if d.IsDir() {
			if sensitiveDirs[d.Name()] {
				s.ExcludedSensitive++
				return fs.SkipDir
			}
			for _, p := range rules.Exclude {
				if strings.HasSuffix(p, "/") && matches(p, rel) {
					s.ExcludedByRule++
					return fs.SkipDir
				}
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		switch {
		case isSensitiveName(d.Name()):
			s.ExcludedSensitive++
			return nil
		case anyMatch(rules.Exclude, rel), len(rules.Include) > 0 && !anyMatch(rules.Include, rel):
			s.ExcludedByRule++
			return nil
		case !supported(rel):
			s.Unsupported++
			return nil
		}
		info, err := d.Info()
		if errors.Is(err, fs.ErrNotExist) {
			return nil // removed during the scan
		}
		if err != nil {
			return err
		}
		s.Files++
		return fn(Entry{Path: rel, Size: info.Size(), ModTime: info.ModTime().UnixNano()})
	})
	return s, err
}
