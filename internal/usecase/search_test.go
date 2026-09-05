package usecase_test

import (
	"context"
	"errors"
	"testing"

	"github.com/sibukixxx/rag-poc/internal/domain/llm"
	"github.com/sibukixxx/rag-poc/internal/domain/retrieval"
	"github.com/sibukixxx/rag-poc/internal/usecase"
)

type fakeVectorSearcher struct {
	results []retrieval.Result
	err     error
}

func (f *fakeVectorSearcher) Search(ctx context.Context, kbID string, vector []float32, topK int) ([]retrieval.Result, error) {
	return f.results, f.err
}

type fakeKeywordSearcher struct {
	results []retrieval.Result
	err     error
}

func (f *fakeKeywordSearcher) Search(ctx context.Context, kbID, query string, topK int) ([]retrieval.Result, error) {
	return f.results, f.err
}

type fakeEmbedder struct {
	vector []float32
	err    error
}

func (f *fakeEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if f.err != nil {
		return nil, f.err
	}
	return [][]float32{f.vector}, nil
}
func (f *fakeEmbedder) Dimensions() int { return len(f.vector) }
func (f *fakeEmbedder) Model() string   { return "test-embed-model" }

type fakeReranker struct {
	reordered []retrieval.Result
	err       error
	called    bool
}

func (f *fakeReranker) Rerank(ctx context.Context, query string, candidates []retrieval.Result, topK int) ([]retrieval.Result, error) {
	f.called = true
	return f.reordered, f.err
}

func TestSearchMergesVectorAndKeywordByRRF(t *testing.T) {
	vec := &fakeVectorSearcher{results: []retrieval.Result{
		{ChunkID: "a", Score: 0.9},
		{ChunkID: "b", Score: 0.8},
		{ChunkID: "c", Score: 0.7},
	}}
	kw := &fakeKeywordSearcher{results: []retrieval.Result{
		{ChunkID: "c", Score: 10},
		{ChunkID: "a", Score: 5},
	}}
	embedder := &fakeEmbedder{vector: []float32{1, 0}}

	uc := usecase.NewSearchUseCase(vec, kw, embedder, nil, nil)
	results, err := uc.Search(context.Background(), "kb-1", "hello", retrieval.Options{TopK: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	// "a" (rank 1 vector, rank 2 keyword) and "c" (rank 3 vector, rank 1
	// keyword) both appear in both lists so should outrank "b" (vector-only).
	if len(results) != 3 {
		t.Fatalf("expected 3 merged results, got %d: %+v", len(results), results)
	}
	if results[2].ChunkID != "b" {
		t.Errorf("expected 'b' (found by only one signal) to rank last, got %+v", results)
	}
}

func TestSearchAppliesTopK(t *testing.T) {
	vec := &fakeVectorSearcher{results: []retrieval.Result{
		{ChunkID: "a"}, {ChunkID: "b"}, {ChunkID: "c"},
	}}
	kw := &fakeKeywordSearcher{}
	embedder := &fakeEmbedder{vector: []float32{1}}

	uc := usecase.NewSearchUseCase(vec, kw, embedder, nil, nil)
	results, err := uc.Search(context.Background(), "kb-1", "hello", retrieval.Options{TopK: 2})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected topK=2 to limit results, got %d", len(results))
	}
}

func TestSearchEmptyQueryIsAnError(t *testing.T) {
	uc := usecase.NewSearchUseCase(&fakeVectorSearcher{}, &fakeKeywordSearcher{}, &fakeEmbedder{}, nil, nil)
	if _, err := uc.Search(context.Background(), "kb-1", "   ", retrieval.Options{}); err == nil {
		t.Fatal("expected an error for a blank query")
	}
}

func TestSearchPropagatesEmbedderError(t *testing.T) {
	embedder := &fakeEmbedder{err: errors.New("embedding service down")}
	uc := usecase.NewSearchUseCase(&fakeVectorSearcher{}, &fakeKeywordSearcher{}, embedder, nil, nil)
	if _, err := uc.Search(context.Background(), "kb-1", "hello", retrieval.Options{}); err == nil {
		t.Fatal("expected an error when the embedder fails")
	}
}

func TestSearchUsesRerankerWhenRequested(t *testing.T) {
	vec := &fakeVectorSearcher{results: []retrieval.Result{{ChunkID: "a"}, {ChunkID: "b"}}}
	kw := &fakeKeywordSearcher{}
	embedder := &fakeEmbedder{vector: []float32{1}}
	reranker := &fakeReranker{reordered: []retrieval.Result{{ChunkID: "b"}, {ChunkID: "a"}}}

	uc := usecase.NewSearchUseCase(vec, kw, embedder, reranker, nil)
	results, err := uc.Search(context.Background(), "kb-1", "hello", retrieval.Options{TopK: 2, Rerank: true})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !reranker.called {
		t.Fatal("expected reranker to be called when Rerank: true")
	}
	if results[0].ChunkID != "b" {
		t.Errorf("expected reranked order to be honored, got %+v", results)
	}
}

func TestSearchSkipsRerankerWhenNotRequested(t *testing.T) {
	vec := &fakeVectorSearcher{results: []retrieval.Result{{ChunkID: "a"}}}
	kw := &fakeKeywordSearcher{}
	embedder := &fakeEmbedder{vector: []float32{1}}
	reranker := &fakeReranker{}

	uc := usecase.NewSearchUseCase(vec, kw, embedder, reranker, nil)
	if _, err := uc.Search(context.Background(), "kb-1", "hello", retrieval.Options{Rerank: false}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if reranker.called {
		t.Fatal("expected reranker NOT to be called when Rerank: false")
	}
}

// llm.Embedder is satisfied by *fakeEmbedder; this is a compile-time check.
var _ llm.Embedder = (*fakeEmbedder)(nil)
