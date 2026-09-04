package usecase

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/text/unicode/norm"

	"github.com/sibukixxx/rag-poc/internal/domain/llm"
	"github.com/sibukixxx/rag-poc/internal/domain/retrieval"
	"github.com/sibukixxx/rag-poc/internal/domain/trace"
)

const (
	// candidatePoolSize is how many results each of vector/keyword search
	// contributes before RRF merging (docs/V0.1_SPEC.md §7).
	candidatePoolSize = 30
	// rrfK is the RRF constant (docs/V0.1_SPEC.md §7: k=60).
	rrfK = 60
	// rerankPoolSize bounds how many merged candidates get sent to the
	// LLM reranker, so a query that matches broadly doesn't turn into an
	// oversized (and expensive) rerank prompt.
	rerankPoolSize = 20
	defaultTopK    = 10
)

// SearchUseCase runs Hybrid Search: embed the query, search vector and
// keyword indexes in parallel-equivalent fashion, merge by Reciprocal
// Rank Fusion, and optionally rerank (docs/V0.1_SPEC.md §7).
type SearchUseCase struct {
	VectorSearcher  retrieval.VectorSearcher
	KeywordSearcher retrieval.KeywordSearcher
	Embedder        llm.Embedder
	Reranker        retrieval.Reranker
	Traces          trace.Store
}

func NewSearchUseCase(vec retrieval.VectorSearcher, kw retrieval.KeywordSearcher, embedder llm.Embedder, reranker retrieval.Reranker, traces trace.Store) *SearchUseCase {
	return &SearchUseCase{VectorSearcher: vec, KeywordSearcher: kw, Embedder: embedder, Reranker: reranker, Traces: traces}
}

func (u *SearchUseCase) Search(ctx context.Context, knowledgeBaseID, query string, opts retrieval.Options) ([]retrieval.Result, error) {
	query = strings.TrimSpace(norm.NFKC.String(query))
	if query == "" {
		return nil, fmt.Errorf("query must not be empty")
	}
	topK := opts.TopK
	if topK <= 0 {
		topK = defaultTopK
	}

	traceID := uuid.NewString()
	traceStart := time.Now()

	embedStart := time.Now()
	vectors, err := u.Embedder.Embed(ctx, []string{query})
	u.recordSpan(traceID, trace.SpanKindEmbed, "embedder.embed", embedStart, u.Embedder.Model(), err)
	if err != nil {
		u.finishTrace(traceID, traceStart, trace.StatusError)
		return nil, fmt.Errorf("embedding query: %w", err)
	}
	if len(vectors) == 0 {
		u.finishTrace(traceID, traceStart, trace.StatusError)
		return nil, fmt.Errorf("no embedding returned for query")
	}

	retrieveStart := time.Now()
	vectorResults, vecErr := u.VectorSearcher.Search(ctx, knowledgeBaseID, vectors[0], candidatePoolSize)
	var keywordResults []retrieval.Result
	var kwErr error
	if vecErr == nil {
		keywordResults, kwErr = u.KeywordSearcher.Search(ctx, knowledgeBaseID, query, candidatePoolSize)
	}
	retrieveErr := vecErr
	if retrieveErr == nil {
		retrieveErr = kwErr
	}
	u.recordSpan(traceID, trace.SpanKindRetrieve, "hybrid.retrieve", retrieveStart, "", retrieveErr)
	if retrieveErr != nil {
		u.finishTrace(traceID, traceStart, trace.StatusError)
		return nil, fmt.Errorf("retrieving candidates: %w", retrieveErr)
	}

	merged := rrfMerge(vectorResults, keywordResults, rrfK)
	if len(merged) > rerankPoolSize {
		merged = merged[:rerankPoolSize]
	}

	if opts.Rerank && u.Reranker != nil && len(merged) > 0 {
		rerankStart := time.Now()
		reranked, err := u.Reranker.Rerank(ctx, query, merged, topK)
		u.recordSpan(traceID, trace.SpanKindRerank, "reranker.rerank", rerankStart, "", err)
		if err == nil && len(reranked) > 0 {
			merged = reranked
		}
	}

	if len(merged) > topK {
		merged = merged[:topK]
	}

	u.finishTrace(traceID, traceStart, trace.StatusOK)
	return merged, nil
}

func (u *SearchUseCase) recordSpan(traceID string, kind trace.SpanKind, name string, start time.Time, model string, err error) {
	if u.Traces == nil {
		return
	}
	status := trace.StatusOK
	errMsg := ""
	if err != nil {
		status = trace.StatusError
		errMsg = err.Error()
	}
	_ = u.Traces.CreateSpan(context.Background(), trace.Span{
		ID: uuid.NewString(), TraceID: traceID, Kind: kind, Name: name,
		StartedAt: start, DurationMS: time.Since(start).Milliseconds(),
		Model: model, Status: status, Error: errMsg,
	})
}

func (u *SearchUseCase) finishTrace(traceID string, start time.Time, status trace.Status) {
	if u.Traces == nil {
		return
	}
	_ = u.Traces.CreateTrace(context.Background(), trace.Trace{
		ID: traceID, Name: "search", StartedAt: start,
		DurationMS: time.Since(start).Milliseconds(), Status: status,
	})
}

// rrfMerge combines the vector and keyword result lists by Reciprocal
// Rank Fusion: a chunk appearing in both lists accumulates a score from
// each, so it naturally outranks a chunk found by only one signal. The
// result's Score field is the summed RRF score, not either input list's
// original score (which used different, incomparable scales — cosine
// similarity vs. bm25).
func rrfMerge(vectorResults, keywordResults []retrieval.Result, k int) []retrieval.Result {
	type accumulator struct {
		result retrieval.Result
		score  float64
	}
	byChunk := make(map[string]*accumulator)
	var order []string

	addList := func(list []retrieval.Result) {
		for rank, r := range list {
			rrfScore := 1.0 / float64(k+rank+1)
			if existing, ok := byChunk[r.ChunkID]; ok {
				existing.score += rrfScore
				continue
			}
			byChunk[r.ChunkID] = &accumulator{result: r, score: rrfScore}
			order = append(order, r.ChunkID)
		}
	}
	addList(vectorResults)
	addList(keywordResults)

	merged := make([]retrieval.Result, len(order))
	for i, id := range order {
		acc := byChunk[id]
		acc.result.Score = acc.score
		merged[i] = acc.result
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].Score > merged[j].Score })
	return merged
}
