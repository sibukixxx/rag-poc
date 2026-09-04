package llmrerank_test

import (
	"context"
	"errors"
	"testing"

	"github.com/sibukixxx/rag-poc/internal/adapter/llmrerank"
	"github.com/sibukixxx/rag-poc/internal/domain/llm"
	"github.com/sibukixxx/rag-poc/internal/domain/retrieval"
)

type fakeLLM struct {
	content string
	err     error
}

func (f *fakeLLM) Generate(ctx context.Context, req llm.GenerateRequest) (*llm.GenerateResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &llm.GenerateResponse{Content: f.content}, nil
}

func (f *fakeLLM) Stream(ctx context.Context, req llm.GenerateRequest) (<-chan llm.StreamEvent, error) {
	panic("not used")
}

func candidates() []retrieval.Result {
	return []retrieval.Result{
		{ChunkID: "a", Text: "first"},
		{ChunkID: "b", Text: "second"},
		{ChunkID: "c", Text: "third"},
	}
}

func newTestRouter(provider llm.LLM) *llm.Router {
	r := llm.NewRouter()
	r.RegisterProvider("default", provider)
	r.RegisterAlias("cheap", llm.Alias{Provider: "default", Model: "gpt-4o-mini"})
	return r
}

func TestRerankReordersByModelResponse(t *testing.T) {
	provider := &fakeLLM{content: "[2, 0, 1]"}
	reranker := llmrerank.New(newTestRouter(provider), "cheap")

	result, err := reranker.Rerank(context.Background(), "q", candidates(), 3)
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	got := []string{result[0].ChunkID, result[1].ChunkID, result[2].ChunkID}
	want := []string{"c", "a", "b"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got order %v, want %v", got, want)
		}
	}
}

func TestRerankRespectsTopK(t *testing.T) {
	provider := &fakeLLM{content: "[2, 0, 1]"}
	reranker := llmrerank.New(newTestRouter(provider), "cheap")

	result, err := reranker.Rerank(context.Background(), "q", candidates(), 2)
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 results, got %d", len(result))
	}
}

func TestRerankIgnoresStrayTextAroundJSON(t *testing.T) {
	provider := &fakeLLM{content: "Sure, here you go: [1, 2, 0] — hope that helps!"}
	reranker := llmrerank.New(newTestRouter(provider), "cheap")

	result, err := reranker.Rerank(context.Background(), "q", candidates(), 3)
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	if result[0].ChunkID != "b" {
		t.Fatalf("got %+v, want chunk 'b' first", result)
	}
}

func TestRerankFallsBackOnLLMError(t *testing.T) {
	provider := &fakeLLM{err: errors.New("boom")}
	reranker := llmrerank.New(newTestRouter(provider), "cheap")

	result, err := reranker.Rerank(context.Background(), "q", candidates(), 2)
	if err != nil {
		t.Fatalf("expected graceful fallback (no error), got %v", err)
	}
	if len(result) != 2 || result[0].ChunkID != "a" || result[1].ChunkID != "b" {
		t.Fatalf("expected original order truncated to topK, got %+v", result)
	}
}

func TestRerankFallsBackOnUnparseableResponse(t *testing.T) {
	provider := &fakeLLM{content: "I refuse to rank these."}
	reranker := llmrerank.New(newTestRouter(provider), "cheap")

	result, err := reranker.Rerank(context.Background(), "q", candidates(), 0)
	if err != nil {
		t.Fatalf("expected graceful fallback (no error), got %v", err)
	}
	if len(result) != 3 {
		t.Fatalf("expected fallback to return all candidates, got %+v", result)
	}
}

func TestRerankEmptyCandidates(t *testing.T) {
	reranker := llmrerank.New(newTestRouter(&fakeLLM{}), "cheap")
	result, err := reranker.Rerank(context.Background(), "q", nil, 5)
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	if len(result) != 0 {
		t.Fatalf("expected empty result for empty candidates, got %+v", result)
	}
}
