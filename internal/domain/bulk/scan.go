// Package bulk models resumable bulk ingestion of a filesystem corpus (#30).
package bulk

import (
	"fmt"
	"path"
	"strings"
)

// Rules are operator include/exclude patterns. A pattern ending in "/" is a
// directory prefix ("drafts/"); any other pattern uses path.Match syntax and
// is tried against both the relative path and the base name ("*.tmp").
// When Include is non-empty, a file must match at least one include.
type Rules struct {
	Include []string `json:"include,omitempty"`
	Exclude []string `json:"exclude,omitempty"`
}

// Entry is one discovered file, identified by its slash-separated path
// relative to the corpus root.
type Entry struct {
	Path    string
	Size    int64
	ModTime int64 // Unix nanoseconds
}

// ScanSummary counts what a scan found and what it left out, so operators
// can see that exclusions happened rather than wondering where files went.
type ScanSummary struct {
	Files             int `json:"files"`
	ExcludedSensitive int `json:"excluded_sensitive"`
	ExcludedByRule    int `json:"excluded_by_rule"`
	Unsupported       int `json:"unsupported"`
	Symlinks          int `json:"symlinks"`
}

// Validate reports malformed patterns up front instead of mid-scan.
func (r Rules) Validate() error {
	for _, set := range []struct {
		kind     string
		patterns []string
	}{{"include", r.Include}, {"exclude", r.Exclude}} {
		for _, p := range set.patterns {
			if strings.HasSuffix(p, "/") {
				continue
			}
			if _, err := path.Match(p, ""); err != nil {
				return fmt.Errorf("%s pattern %q: %w", set.kind, p, err)
			}
		}
	}
	return nil
}
