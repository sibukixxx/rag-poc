package usecase

import (
	"strings"
	"testing"

	"github.com/sibukixxx/rag-poc/internal/adapter/tokenizer"
	"github.com/sibukixxx/rag-poc/internal/domain/retrieval"
)

func repeatWords(n int) string {
	words := make([]string, n)
	for i := range words {
		words[i] = "word"
	}
	return strings.Join(words, " ")
}

func TestBuildContextIncludesAllWithinBudget(t *testing.T) {
	tok, err := tokenizer.New()
	if err != nil {
		t.Fatalf("tokenizer.New: %v", err)
	}
	page := 3
	results := []retrieval.Result{
		{ChunkID: "a", DocumentID: "doc-a", Filename: "a.md", Text: "short text one", Page: &page},
		{ChunkID: "b", DocumentID: "doc-b", Filename: "b.md", Text: "short text two"},
	}

	chunks, text := buildContext(results, tok, defaultContextTokenBudget)
	if len(chunks) != 2 {
		t.Fatalf("expected both chunks included, got %d", len(chunks))
	}
	if chunks[0].Index != 1 || chunks[1].Index != 2 {
		t.Errorf("expected 1-based sequential indices, got %+v", chunks)
	}
	if chunks[0].Page == nil || *chunks[0].Page != 3 {
		t.Errorf("expected page carried through, got %+v", chunks[0])
	}
	if !strings.Contains(text, "[1]") || !strings.Contains(text, "[2]") {
		t.Errorf("expected numbered citation markers in context text, got %q", text)
	}
}

func TestBuildContextTruncatesAtBudget(t *testing.T) {
	tok, err := tokenizer.New()
	if err != nil {
		t.Fatalf("tokenizer.New: %v", err)
	}
	results := []retrieval.Result{
		{ChunkID: "a", Filename: "a.md", Text: repeatWords(50)},
		{ChunkID: "b", Filename: "b.md", Text: repeatWords(50)},
		{ChunkID: "c", Filename: "c.md", Text: repeatWords(50)},
	}

	// Budget only large enough for the first chunk (~50 tokens) plus a
	// little slack, not two.
	chunks, _ := buildContext(results, tok, 60)
	if len(chunks) != 1 {
		t.Fatalf("expected truncation to 1 chunk, got %d", len(chunks))
	}
	if chunks[0].ChunkID != "a" {
		t.Errorf("expected the first (best-ranked) chunk to be kept, got %s", chunks[0].ChunkID)
	}
}

func TestBuildContextAlwaysIncludesFirstChunkEvenIfOversized(t *testing.T) {
	tok, err := tokenizer.New()
	if err != nil {
		t.Fatalf("tokenizer.New: %v", err)
	}
	results := []retrieval.Result{
		{ChunkID: "huge", Filename: "huge.md", Text: repeatWords(500)},
	}

	chunks, _ := buildContext(results, tok, 10) // budget far smaller than the chunk
	if len(chunks) != 1 {
		t.Fatalf("expected the single oversized chunk to still be included, got %d chunks", len(chunks))
	}
}

func TestBuildContextEmptyResults(t *testing.T) {
	tok, err := tokenizer.New()
	if err != nil {
		t.Fatalf("tokenizer.New: %v", err)
	}
	chunks, text := buildContext(nil, tok, defaultContextTokenBudget)
	if len(chunks) != 0 {
		t.Errorf("expected no chunks for empty results, got %+v", chunks)
	}
	if text != "" {
		t.Errorf("expected empty context text, got %q", text)
	}
}
