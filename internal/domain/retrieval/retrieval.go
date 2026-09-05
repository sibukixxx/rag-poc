// Package retrieval defines Hybrid Search's contracts: a vector searcher,
// a keyword searcher, and an optional reranker, merged by RRF in
// usecase.SearchUseCase (docs/V0.1_SPEC.md §7).
package retrieval

import "context"

// Result is one retrieved chunk, carrying enough origin metadata to build
// a citation.
type Result struct {
	ChunkID    string
	DocumentID string
	Filename   string
	Text       string
	Score      float64
	Page       *int
	Heading    string
}

type Options struct {
	TopK   int
	Rerank bool
}

// VectorSearcher finds chunks by embedding similarity within one
// knowledge base. The embedded (v0.1) implementation is a brute-force
// cosine scan (internal/adapter/vecmem); a pgvector/Qdrant adapter can
// implement the same interface later without changing the search usecase.
type VectorSearcher interface {
	Search(ctx context.Context, knowledgeBaseID string, queryVector []float32, topK int) ([]Result, error)
}

// KeywordSearcher finds chunks by lexical match (FTS5 trigram in v0.1;
// see docs/DESIGN_REVIEW.md F-4 for why trigram, and why very short
// queries need a fallback).
type KeywordSearcher interface {
	Search(ctx context.Context, knowledgeBaseID, query string, topK int) ([]Result, error)
}

// Reranker re-scores/reorders a candidate list against the original
// query. v0.1's only implementation is LLM listwise reranking
// (docs/DESIGN_REVIEW.md F-7); a cross-encoder API adapter can implement
// the same interface later.
type Reranker interface {
	Rerank(ctx context.Context, query string, candidates []Result, topK int) ([]Result, error)
}
