package tokenizer

import (
	"strings"

	"github.com/sibukixxx/rag-poc/internal/domain/knowledge"
)

// ChunkerConfig controls chunk sizing. Chunking happens per Page: a page
// under MaxTokens becomes one chunk (keeping its page/heading intact for
// citations); a larger page is split into overlapping windows.
type ChunkerConfig struct {
	MaxTokens int
	Overlap   int
}

// DefaultChunkerConfig mirrors common RAG defaults: ~500-token chunks
// with a 50-token overlap so context isn't lost at a window boundary.
func DefaultChunkerConfig() ChunkerConfig {
	return ChunkerConfig{MaxTokens: 500, Overlap: 50}
}

func (c ChunkerConfig) normalized() ChunkerConfig {
	if c.MaxTokens <= 0 {
		c.MaxTokens = 500
	}
	if c.Overlap < 0 || c.Overlap >= c.MaxTokens {
		c.Overlap = 0
	}
	return c
}

// ChunkResult is a chunk of text plus enough origin metadata to build a
// citation later.
type ChunkResult struct {
	Text       string
	TokenCount int
	Page       *int
	Heading    string
}

// ChunkPages splits each page's text into token-bounded chunks. Chunking
// is deterministic for a given (pages, config) pair, which is what makes
// content hashing meaningful (docs/V0.1_SPEC.md §3).
func (t *Tokenizer) ChunkPages(pages []knowledge.Page, cfg ChunkerConfig) []ChunkResult {
	cfg = cfg.normalized()
	step := cfg.MaxTokens - cfg.Overlap
	if step <= 0 {
		step = cfg.MaxTokens
	}

	var results []ChunkResult
	for _, page := range pages {
		text := strings.TrimSpace(page.Text)
		if text == "" {
			continue
		}

		tokens := t.enc.Encode(text, nil, nil)
		if len(tokens) == 0 {
			continue
		}

		var pageNum *int
		if page.Number > 0 {
			n := page.Number
			pageNum = &n
		}

		for start := 0; start < len(tokens); start += step {
			end := min(start+cfg.MaxTokens, len(tokens))
			chunkTokens := tokens[start:end]
			results = append(results, ChunkResult{
				Text:       t.enc.Decode(chunkTokens),
				TokenCount: len(chunkTokens),
				Page:       pageNum,
				Heading:    page.Heading,
			})
			if end == len(tokens) {
				break
			}
		}
	}
	return results
}
