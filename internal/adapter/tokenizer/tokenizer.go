// Package tokenizer counts and chunks text using a real tiktoken
// encoding (cl100k_base), loaded from an embedded offline BPE dataset so
// ForgeAI never needs network access to tokenize — consistent with the
// single-binary, local-first design (docs/DESIGN_REVIEW.md F-11).
package tokenizer

import (
	"fmt"
	"sync"

	"github.com/pkoukk/tiktoken-go"
	tiktokenloader "github.com/pkoukk/tiktoken-go-loader"
)

var setLoaderOnce sync.Once

func ensureOfflineLoader() {
	setLoaderOnce.Do(func() {
		tiktoken.SetBpeLoader(tiktokenloader.NewOfflineLoader())
	})
}

type Tokenizer struct {
	enc *tiktoken.Tiktoken
}

// New builds a Tokenizer using the cl100k_base encoding (GPT-3.5/4 family).
// It never touches the network.
func New() (*Tokenizer, error) {
	ensureOfflineLoader()
	enc, err := tiktoken.GetEncoding("cl100k_base")
	if err != nil {
		return nil, fmt.Errorf("tokenizer: loading cl100k_base: %w", err)
	}
	return &Tokenizer{enc: enc}, nil
}

func (t *Tokenizer) Count(text string) int {
	if text == "" {
		return 0
	}
	return len(t.enc.Encode(text, nil, nil))
}
