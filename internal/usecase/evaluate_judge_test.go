package usecase_test

import (
	"context"
	"errors"
	"testing"

	"github.com/sibukixxx/rag-poc/internal/adapter/tokenizer"
	"github.com/sibukixxx/rag-poc/internal/domain/eval"
	"github.com/sibukixxx/rag-poc/internal/domain/llm"
	"github.com/sibukixxx/rag-poc/internal/domain/retrieval"
	"github.com/sibukixxx/rag-poc/internal/usecase"
)

// judgeEvalFixture wires an EvaluationUseCase whose "normal" alias answers
// with answerer and whose "judge" alias grades with judge.
func judgeEvalFixture(t *testing.T, store *memEvalStore, answerer, judge llm.LLM) *usecase.EvaluationUseCase {
	t.Helper()
	kw := &fakeQueryKeywordSearcher{byQuery: map[string][]retrieval.Result{
		"returns query": {{ChunkID: "c1", Filename: "returns.md", Text: "返品は30日以内。"}},
		"other query":   {{ChunkID: "c2", Filename: "faq.md", Text: "FAQ。"}},
	}}
	search := usecase.NewSearchUseCase(emptyVectorSearcher{}, kw, &fakeEmbedder{vector: []float32{1}}, nil, nil)

	router := llm.NewRouter()
	router.RegisterProvider("answerer", answerer)
	router.RegisterProvider("grader", judge)
	router.RegisterAlias("normal", llm.Alias{Provider: "answerer", Model: "gpt-4o-mini"})
	router.RegisterAlias("judge", llm.Alias{Provider: "grader", Model: "gpt-4o-mini"})

	tok, err := tokenizer.New()
	if err != nil {
		t.Fatalf("tokenizer.New: %v", err)
	}
	traces := newMemTraceStore()
	rag := usecase.NewRAGChatUseCase(search, router, testPrices(), traces, tok, nil)
	grader := usecase.NewLLMJudge(router, testPrices(), traces, nil)
	return usecase.NewEvaluationUseCase(search, rag, grader, store, traces)
}

func TestEvaluationJudgeRunScoresAnswersAndAggregates(t *testing.T) {
	dataset := eval.Dataset{ID: "ds-1", Name: "demo-golden", KnowledgeBaseID: "kb-1"}
	store := newMemEvalStore(dataset, []eval.Case{
		{ID: "case-1", DatasetID: "ds-1", Query: "returns query", ExpectedFilenames: []string{"returns.md"}, ExpectedAnswer: "30日以内"},
		{ID: "case-2", DatasetID: "ds-1", Query: "other query", ExpectedFilenames: []string{"returns.md"}},
	})
	answerer := &fakeLLM{genResp: &llm.GenerateResponse{
		Content: "30日以内です [1]", Usage: llm.Usage{InputTokens: 100, OutputTokens: 10},
	}}
	judge := &fakeLLM{genResp: &llm.GenerateResponse{
		Content: `{"correctness": 1.0, "groundedness": 0.5, "relevance": 0.8, "reason": "ok"}`,
		Usage:   llm.Usage{InputTokens: 200, OutputTokens: 20},
	}}
	uc := judgeEvalFixture(t, store, answerer, judge)

	run, err := uc.CreateRun(context.Background(), dataset.ID, usecase.RunOptions{TopK: 10, Judge: true})
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if !run.Judge || run.Alias != "normal" {
		t.Fatalf("expected a judge run defaulting to alias normal, got %+v", run)
	}
	if err := uc.Execute(context.Background(), run.ID); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	final, _ := store.GetRun(context.Background(), run.ID)
	if final.Status != eval.RunStatusDone {
		t.Fatalf("expected done, got %s (%s)", final.Status, final.Error)
	}
	// Both cases judged identically: means equal the judge's scores.
	if final.Correctness != 1.0 || final.Groundedness != 0.5 || final.Relevance != 0.8 {
		t.Errorf("unexpected judge aggregates: %+v", final)
	}
	// Retrieval is still scored: case-1 hits, case-2 misses.
	if final.HitRate != 0.5 {
		t.Errorf("expected HitRate 0.5, got %v", final.HitRate)
	}
	if final.CostUSD <= 0 {
		t.Error("expected the run to accumulate answer + judge cost")
	}

	results, _ := store.ListCaseResults(context.Background(), run.ID)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for _, r := range results {
		if r.Answer != "30日以内です [1]" || r.JudgeReason != "ok" || r.JudgeModel != "gpt-4o-mini" {
			t.Errorf("answer/judge fields not recorded: %+v", r)
		}
		if r.CostUSD <= 0 || r.Error != "" {
			t.Errorf("expected per-case cost and no error: %+v", r)
		}
	}
}

func TestEvaluationJudgeRunRecordsPerCaseJudgeErrors(t *testing.T) {
	dataset := eval.Dataset{ID: "ds-1", Name: "demo-golden", KnowledgeBaseID: "kb-1"}
	store := newMemEvalStore(dataset, []eval.Case{
		{ID: "case-1", DatasetID: "ds-1", Query: "returns query", ExpectedFilenames: []string{"returns.md"}},
	})
	answerer := &fakeLLM{genResp: &llm.GenerateResponse{Content: "30日以内です [1]"}}
	judge := &fakeLLM{genErr: errors.New("judge provider down")}
	uc := judgeEvalFixture(t, store, answerer, judge)

	run, _ := uc.CreateRun(context.Background(), dataset.ID, usecase.RunOptions{Judge: true})
	if err := uc.Execute(context.Background(), run.ID); err != nil {
		t.Fatalf("Execute should not abort on a per-case judge failure: %v", err)
	}
	final, _ := store.GetRun(context.Background(), run.ID)
	if final.Status != eval.RunStatusDone {
		t.Fatalf("expected done (fail-soft), got %s", final.Status)
	}
	results, _ := store.ListCaseResults(context.Background(), run.ID)
	if results[0].Error == "" || results[0].Answer == "" || results[0].Hit != true {
		t.Errorf("expected the answer and retrieval score kept with a judge error recorded, got %+v", results[0])
	}
}

func TestEvaluationJudgeRunRequiresPipeline(t *testing.T) {
	dataset := eval.Dataset{ID: "ds-1", Name: "demo-golden", KnowledgeBaseID: "kb-1"}
	store := newMemEvalStore(dataset, nil)
	search := usecase.NewSearchUseCase(emptyVectorSearcher{}, &fakeQueryKeywordSearcher{}, &fakeEmbedder{vector: []float32{1}}, nil, nil)
	uc := usecase.NewEvaluationUseCase(search, nil, nil, store, nil)
	if _, err := uc.CreateRun(context.Background(), dataset.ID, usecase.RunOptions{Judge: true}); err == nil {
		t.Fatal("expected CreateRun to refuse a judge run without RAG/judge wired")
	}
}
