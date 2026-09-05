package usecase_test

import (
	"context"
	"errors"
	"testing"

	"github.com/sibukixxx/rag-poc/internal/adapter/tokenizer"
	"github.com/sibukixxx/rag-poc/internal/domain/llm"
	"github.com/sibukixxx/rag-poc/internal/domain/retrieval"
	"github.com/sibukixxx/rag-poc/internal/domain/trace"
	"github.com/sibukixxx/rag-poc/internal/usecase"
)

func intPtr(n int) *int { return &n }

func TestRAGChatStreamAttachesCitationsOnDone(t *testing.T) {
	vec := &fakeVectorSearcher{results: []retrieval.Result{
		{ChunkID: "c1", DocumentID: "d1", Filename: "policy.md", Text: "Returns allowed within 30 days.", Page: intPtr(1)},
	}}
	embedder := &fakeEmbedder{vector: []float32{1}}
	searchUC := usecase.NewSearchUseCase(vec, &fakeKeywordSearcher{}, embedder, nil, nil)

	provider := &fakeLLM{streamEvts: []llm.StreamEvent{
		{Delta: "You can return items "},
		{Delta: "within 30 days [1]."},
		{Done: true, Usage: llm.Usage{InputTokens: 50, OutputTokens: 10}},
	}}
	router := testRouter(t, provider)
	traces := newMemTraceStore()
	tok, err := tokenizer.New()
	if err != nil {
		t.Fatalf("tokenizer.New: %v", err)
	}

	ragUC := usecase.NewRAGChatUseCase(searchUC, router, testPrices(), traces, tok, nil)
	result, err := ragUC.ChatStream(context.Background(), "kb-1", "normal", "What is the return policy?", false)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}

	var content string
	var final usecase.RAGStreamEvent
	for ev := range result.Events {
		if ev.Err != nil {
			t.Fatalf("unexpected error: %v", ev.Err)
		}
		content += ev.Delta
		if ev.Done {
			final = ev
		}
	}

	if content != "You can return items within 30 days [1]." {
		t.Errorf("got content %q", content)
	}
	if len(final.Citations) != 1 || final.Citations[0].Filename != "policy.md" || final.Citations[0].Index != 1 {
		t.Fatalf("got citations %+v", final.Citations)
	}
	if final.NoContext {
		t.Error("expected NoContext=false when a citation was used")
	}
	if final.CostUSD <= 0 {
		t.Error("expected a positive cost for a non-empty usage")
	}

	tr, spans, err := traces.GetTrace(context.Background(), result.TraceID)
	if err != nil {
		t.Fatalf("GetTrace: %v", err)
	}
	if tr.Status != trace.StatusOK {
		t.Errorf("expected trace status ok, got %s", tr.Status)
	}
	if spans[0].InputTokens != 50 || spans[0].OutputTokens != 10 {
		t.Errorf("got span %+v", spans[0])
	}
}

func TestRAGChatStreamNoContextWhenNoResults(t *testing.T) {
	embedder := &fakeEmbedder{vector: []float32{1}}
	searchUC := usecase.NewSearchUseCase(&fakeVectorSearcher{}, &fakeKeywordSearcher{}, embedder, nil, nil)

	provider := &fakeLLM{streamEvts: []llm.StreamEvent{
		{Delta: "I don't know."},
		{Done: true},
	}}
	router := testRouter(t, provider)
	tok, err := tokenizer.New()
	if err != nil {
		t.Fatalf("tokenizer.New: %v", err)
	}

	ragUC := usecase.NewRAGChatUseCase(searchUC, router, testPrices(), newMemTraceStore(), tok, nil)
	result, err := ragUC.ChatStream(context.Background(), "kb-1", "normal", "anything", false)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}

	var final usecase.RAGStreamEvent
	for ev := range result.Events {
		if ev.Done {
			final = ev
		}
	}
	if !final.NoContext {
		t.Error("expected NoContext=true when retrieval returns nothing")
	}
	if len(final.Citations) != 0 {
		t.Errorf("expected no citations, got %+v", final.Citations)
	}
}

func TestRAGChatStreamPropagatesSearchError(t *testing.T) {
	embedder := &fakeEmbedder{err: errors.New("boom")}
	searchUC := usecase.NewSearchUseCase(&fakeVectorSearcher{}, &fakeKeywordSearcher{}, embedder, nil, nil)
	router := testRouter(t, &fakeLLM{})
	tok, err := tokenizer.New()
	if err != nil {
		t.Fatalf("tokenizer.New: %v", err)
	}

	ragUC := usecase.NewRAGChatUseCase(searchUC, router, testPrices(), newMemTraceStore(), tok, nil)
	if _, err := ragUC.ChatStream(context.Background(), "kb-1", "normal", "q", false); err == nil {
		t.Fatal("expected an error when retrieval fails")
	}
}

func TestRAGChatStreamUnknownAlias(t *testing.T) {
	embedder := &fakeEmbedder{vector: []float32{1}}
	searchUC := usecase.NewSearchUseCase(&fakeVectorSearcher{}, &fakeKeywordSearcher{}, embedder, nil, nil)
	tok, err := tokenizer.New()
	if err != nil {
		t.Fatalf("tokenizer.New: %v", err)
	}

	ragUC := usecase.NewRAGChatUseCase(searchUC, llm.NewRouter(), testPrices(), newMemTraceStore(), tok, nil)
	if _, err := ragUC.ChatStream(context.Background(), "kb-1", "nonexistent", "q", false); err == nil {
		t.Fatal("expected an error for an unknown alias")
	}
}
