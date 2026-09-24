package fsscan

import (
	"fmt"
	"io"
	"os"
)

// LocalCorpus scans and reads a directory on the ForgeAI host. It opens the
// root with os.OpenRoot, so neither ".." paths nor symbolic links can reach
// files outside it — even if a file is swapped for a link after the scan.
type LocalCorpus struct {
	Supported func(path string) bool
}

func (c LocalCorpus) Scan(root string, rules Rules, fn func(Entry) error) (Summary, error) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return Summary{}, fmt.Errorf("opening corpus root: %w", err)
	}
	defer r.Close()
	return Scan(r.FS(), rules, c.Supported, fn)
}

// ReadFile reads one discovered file, refusing files larger than maxBytes.
func (c LocalCorpus) ReadFile(root, rel string, maxBytes int64) ([]byte, error) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("opening corpus root: %w", err)
	}
	defer r.Close()
	f, err := r.Open(rel)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", rel)
	}
	if info.Size() > maxBytes {
		return nil, fmt.Errorf("%s is %d bytes, over the %d-byte limit", rel, info.Size(), maxBytes)
	}
	// The limit also bounds a file that grows between Stat and Read.
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("%s grew past the %d-byte limit while being read", rel, maxBytes)
	}
	return data, nil
}
