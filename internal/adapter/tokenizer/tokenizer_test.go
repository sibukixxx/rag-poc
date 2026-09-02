package tokenizer_test

import (
	"testing"

	"github.com/sibukixxx/rag-poc/internal/adapter/tokenizer"
	"github.com/sibukixxx/rag-poc/internal/domain/knowledge"
)

func TestCount(t *testing.T) {
	tok, err := tokenizer.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := tok.Count(""); got != 0 {
		t.Errorf("Count(\"\") = %d, want 0", got)
	}
	if got := tok.Count("hello world"); got == 0 {
		t.Errorf("Count(\"hello world\") = 0, want > 0")
	}
}

func TestChunkPagesSingleSmallPage(t *testing.T) {
	tok, err := tokenizer.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	pages := []knowledge.Page{{Number: 3, Heading: "Intro", Text: "This is a short page."}}
	results := tok.ChunkPages(pages, tokenizer.DefaultChunkerConfig())
	if len(results) != 1 {
		t.Fatalf("expected 1 chunk for a short page, got %d", len(results))
	}
	if results[0].Page == nil || *results[0].Page != 3 {
		t.Errorf("expected page 3, got %+v", results[0].Page)
	}
	if results[0].Heading != "Intro" {
		t.Errorf("expected heading preserved, got %q", results[0].Heading)
	}
}

func TestChunkPagesSplitsLargePageWithOverlap(t *testing.T) {
	tok, err := tokenizer.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Build a page with clearly more tokens than MaxTokens.
	words := make([]string, 0, 300)
	for i := 0; i < 300; i++ {
		words = append(words, "word")
	}
	text := ""
	for i, w := range words {
		if i > 0 {
			text += " "
		}
		text += w
	}

	cfg := tokenizer.ChunkerConfig{MaxTokens: 50, Overlap: 10}
	results := tok.ChunkPages([]knowledge.Page{{Number: 1, Text: text}}, cfg)
	if len(results) < 2 {
		t.Fatalf("expected multiple chunks for a large page, got %d", len(results))
	}
	for _, r := range results {
		if r.TokenCount > cfg.MaxTokens {
			t.Errorf("chunk exceeds MaxTokens: %d > %d", r.TokenCount, cfg.MaxTokens)
		}
	}
}

func TestChunkPagesIsDeterministic(t *testing.T) {
	tok, err := tokenizer.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	pages := []knowledge.Page{{Number: 1, Text: "The quick brown fox jumps over the lazy dog."}}
	cfg := tokenizer.DefaultChunkerConfig()

	a := tok.ChunkPages(pages, cfg)
	b := tok.ChunkPages(pages, cfg)
	if len(a) != len(b) {
		t.Fatalf("non-deterministic chunk count: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].Text != b[i].Text {
			t.Errorf("non-deterministic chunk text at %d: %q vs %q", i, a[i].Text, b[i].Text)
		}
	}
}
