package usecase_test

import (
	"context"
	"errors"
	"testing"

	"github.com/sibukixxx/rag-poc/internal/domain/llm"
	"github.com/sibukixxx/rag-poc/internal/domain/trace"
	"github.com/sibukixxx/rag-poc/internal/usecase"
)

func judgeRouter(t *testing.T, provider llm.LLM) *llm.Router {
	t.Helper()
	r := llm.NewRouter()
	r.RegisterProvider("default", provider)
	r.RegisterAlias("judge", llm.Alias{Provider: "default", Model: "gpt-4o-mini"})
	return r
}

func TestLLMJudgeParsesScoresAndRecordsSpan(t *testing.T) {
	provider := &fakeLLM{genResp: &llm.GenerateResponse{
		Content: "Sure, here is my verdict:\n```json\n{\"correctness\": 0.9, \"groundedness\": 1.0, \"relevance\": 0.8, \"reason\": \"cites [1] correctly\"}\n```",
		Usage:   llm.Usage{InputTokens: 400, OutputTokens: 40},
	}}
	traces := newMemTraceStore()
	// The judge attaches its span to a caller-owned trace (the eval run's
	// per-case trace), so that trace exists before judging.
	_ = traces.CreateTrace(context.Background(), trace.Trace{ID: "trace-1", Name: "eval_case"})
	judge := usecase.NewLLMJudge(judgeRouter(t, provider), testPrices(), traces, nil)

	got, err := judge.Judge(context.Background(), usecase.JudgeInput{
		Question: "返品はいつまで？", Context: "[1] source: returns.md\n30日以内", Answer: "30日以内です [1]",
	}, "trace-1")
	if err != nil {
		t.Fatalf("Judge: %v", err)
	}
	if got.Correctness != 0.9 || got.Groundedness != 1.0 || got.Relevance != 0.8 {
		t.Errorf("unexpected scores: %+v", got)
	}
	if got.Reason != "cites [1] correctly" {
		t.Errorf("unexpected reason: %q", got.Reason)
	}
	if got.Model != "gpt-4o-mini" || got.PromptVersion != 0 {
		t.Errorf("expected model gpt-4o-mini / prompt version 0 (no registry), got %+v", got)
	}
	if got.CostUSD <= 0 {
		t.Error("expected a positive cost")
	}

	_, spans, _ := traces.GetTrace(context.Background(), "trace-1")
	if len(spans) != 1 || spans[0].Kind != trace.SpanKindJudge || spans[0].InputTokens != 400 {
		t.Errorf("expected one judge span with usage on trace-1, got %+v", spans)
	}
}

func TestLLMJudgeClampsOutOfRangeScores(t *testing.T) {
	provider := &fakeLLM{genResp: &llm.GenerateResponse{
		Content: `{"correctness": 1.7, "groundedness": -0.2, "relevance": 0.5, "reason": "x"}`,
	}}
	judge := usecase.NewLLMJudge(judgeRouter(t, provider), testPrices(), nil, nil)
	got, err := judge.Judge(context.Background(), usecase.JudgeInput{Question: "q", Answer: "a"}, "")
	if err != nil {
		t.Fatalf("Judge: %v", err)
	}
	if got.Correctness != 1 || got.Groundedness != 0 {
		t.Errorf("expected scores clamped to [0,1], got %+v", got)
	}
}

func TestLLMJudgeErrorsOnUnparseableResponse(t *testing.T) {
	provider := &fakeLLM{genResp: &llm.GenerateResponse{Content: "I cannot evaluate this."}}
	judge := usecase.NewLLMJudge(judgeRouter(t, provider), testPrices(), nil, nil)
	if _, err := judge.Judge(context.Background(), usecase.JudgeInput{Question: "q", Answer: "a"}, ""); err == nil {
		t.Fatal("expected an error when the response has no JSON object")
	}

	missing := &fakeLLM{genResp: &llm.GenerateResponse{Content: `{"correctness": 1.0, "reason": "partial"}`}}
	judge = usecase.NewLLMJudge(judgeRouter(t, missing), testPrices(), nil, nil)
	if _, err := judge.Judge(context.Background(), usecase.JudgeInput{Question: "q", Answer: "a"}, ""); err == nil {
		t.Fatal("expected an error when a score dimension is missing")
	}
}

func TestLLMJudgePropagatesProviderError(t *testing.T) {
	provider := &fakeLLM{genErr: errors.New("provider down")}
	judge := usecase.NewLLMJudge(judgeRouter(t, provider), testPrices(), nil, nil)
	if _, err := judge.Judge(context.Background(), usecase.JudgeInput{Question: "q", Answer: "a"}, ""); err == nil {
		t.Fatal("expected the provider error to propagate")
	}
}
