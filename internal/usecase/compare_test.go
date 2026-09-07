package usecase_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sibukixxx/rag-poc/internal/domain/eval"
	"github.com/sibukixxx/rag-poc/internal/usecase"
)

func compareFixture() (eval.Dataset, []eval.Case, eval.Run, []eval.CaseResult, eval.Run, []eval.CaseResult) {
	ds := eval.Dataset{ID: "ds", Name: "demo-golden", KnowledgeBaseID: "kb"}
	cases := []eval.Case{
		{ID: "c1", DatasetID: "ds", Query: "q1"},
		{ID: "c2", DatasetID: "ds", Query: "q2"},
		{ID: "c3", DatasetID: "ds", Query: "q3 | with pipe"},
	}
	now := time.Now()
	runA := eval.Run{ID: "run-a", DatasetID: "ds", Status: eval.RunStatusDone, TopK: 10, Judge: true, Alias: "cheap",
		HitRate: 2.0 / 3, MRR: 0.5, RecallAtK: 2.0 / 3, PrecisionAtK: 0.1, Correctness: 0.6, Groundedness: 0.7, Relevance: 0.8, StartedAt: now}
	resultsA := []eval.CaseResult{
		{RunID: "run-a", CaseID: "c1", Hit: true, ReciprocalRank: 1, Correctness: 0.9, Groundedness: 0.9, Relevance: 0.9, CostUSD: 0.001, DurationMS: 100},
		{RunID: "run-a", CaseID: "c2", Hit: false, ReciprocalRank: 0, Correctness: 0.2, Groundedness: 0.5, Relevance: 0.6, CostUSD: 0.001, DurationMS: 200},
		{RunID: "run-a", CaseID: "c3", Hit: true, ReciprocalRank: 0.5, Correctness: 0.7, Groundedness: 0.7, Relevance: 0.9, CostUSD: 0.001, DurationMS: 900},
	}
	runB := eval.Run{ID: "run-b", DatasetID: "ds", Status: eval.RunStatusDone, TopK: 10, Rerank: true, Judge: true, Alias: "normal",
		HitRate: 1, MRR: 0.83, RecallAtK: 1, PrecisionAtK: 0.1, Correctness: 0.8, Groundedness: 0.8, Relevance: 0.85, StartedAt: now.Add(time.Minute)}
	resultsB := []eval.CaseResult{
		{RunID: "run-b", CaseID: "c1", Hit: true, ReciprocalRank: 1, Correctness: 0.9, Groundedness: 0.9, Relevance: 0.9, CostUSD: 0.002, DurationMS: 150},
		{RunID: "run-b", CaseID: "c2", Hit: true, ReciprocalRank: 0.5, Correctness: 0.8, Groundedness: 0.8, Relevance: 0.8, CostUSD: 0.002, DurationMS: 250},
		{RunID: "run-b", CaseID: "c3", Hit: true, ReciprocalRank: 1, Correctness: 0.7, Groundedness: 0.7, Relevance: 0.85, CostUSD: 0.002, DurationMS: 300},
	}
	return ds, cases, runA, resultsA, runB, resultsB
}

func TestBuildComparisonSummariesWinnerAndCaseDeltas(t *testing.T) {
	ds, cases, runA, resultsA, runB, resultsB := compareFixture()
	c := usecase.BuildComparison(ds, runA, resultsA, runB, resultsB, cases)

	if !c.Judged {
		t.Fatal("expected Judged=true when both runs are judge runs")
	}
	// Latency: A durations 100,200,900 -> avg 400, p95 (nearest rank of 3) = 900.
	if c.A.AvgLatencyMS != 400 || c.A.P95LatencyMS != 900 {
		t.Errorf("A latency: avg=%v p95=%v", c.A.AvgLatencyMS, c.A.P95LatencyMS)
	}
	if c.B.TotalCostUSD < 0.0059 || c.B.TotalCostUSD > 0.0061 {
		t.Errorf("B total cost = %v", c.B.TotalCostUSD)
	}
	if c.Winner != "b" {
		t.Errorf("expected B to win on quality, got %s (%s)", c.Winner, c.Rationale)
	}

	byKey := map[string]usecase.MetricDelta{}
	for _, m := range c.Metrics {
		byKey[m.Key] = m
	}
	if byKey["hit_rate"].Winner != "b" || byKey["correctness"].Winner != "b" {
		t.Errorf("expected B to win hit_rate and correctness: %+v %+v", byKey["hit_rate"], byKey["correctness"])
	}
	if byKey["total_cost_usd"].Winner != "a" || byKey["total_cost_usd"].HigherIsBetter {
		t.Errorf("expected A (cheaper) to win cost with HigherIsBetter=false: %+v", byKey["total_cost_usd"])
	}
	if byKey["precision_at_k"].Winner != "tie" {
		t.Errorf("expected identical precision to tie: %+v", byKey["precision_at_k"])
	}

	// c1 unchanged, c2 improved (miss->hit), c3: RR up, judge mean slightly down -> mixed.
	verdicts := map[string]string{}
	for _, cd := range c.Cases {
		verdicts[cd.CaseID] = cd.Verdict
	}
	if verdicts["c1"] != "same" || verdicts["c2"] != "improved" || verdicts["c3"] != "mixed" {
		t.Errorf("unexpected verdicts: %+v", verdicts)
	}
	if c.Improved != 1 || c.Regressed != 0 {
		t.Errorf("improved=%d regressed=%d", c.Improved, c.Regressed)
	}
}

func TestBuildComparisonTieBreaksOnCost(t *testing.T) {
	ds, cases, runA, resultsA, _, _ := compareFixture()
	runB := runA
	runB.ID = "run-b"
	resultsB := make([]eval.CaseResult, len(resultsA))
	copy(resultsB, resultsA)
	for i := range resultsB {
		resultsB[i].RunID = "run-b"
		resultsB[i].CostUSD = resultsA[i].CostUSD / 2
	}
	c := usecase.BuildComparison(ds, runA, resultsA, runB, resultsB, cases)
	if c.Winner != "b" || !strings.Contains(c.Rationale, "cheaper") {
		t.Errorf("expected B to win on cost with quality tied, got %s (%s)", c.Winner, c.Rationale)
	}
}

func TestBuildComparisonHidesJudgeMetricsUnlessBothJudged(t *testing.T) {
	ds, cases, runA, resultsA, runB, resultsB := compareFixture()
	runB.Judge = false
	c := usecase.BuildComparison(ds, runA, resultsA, runB, resultsB, cases)
	if c.Judged {
		t.Fatal("expected Judged=false when only one run was judged")
	}
	for _, m := range c.Metrics {
		if m.Key == "correctness" && m.Available {
			t.Error("expected correctness to be unavailable")
		}
	}
	md := usecase.RenderComparisonMarkdown(c)
	if strings.Contains(md, "Correctness") {
		t.Error("markdown should omit unavailable judge rows")
	}
}

func TestRenderComparisonMarkdown(t *testing.T) {
	ds, cases, runA, resultsA, runB, resultsB := compareFixture()
	c := usecase.BuildComparison(ds, runA, resultsA, runB, resultsB, cases)
	md := usecase.RenderComparisonMarkdown(c)

	for _, want := range []string{
		"# Evaluation comparison: demo-golden",
		"| Hit Rate | 0.667 | 1.000 | +0.333 | B |",
		"| P95 latency (ms) | 900 | 300 | −600 | B |",
		"**Winner: B**",
		"## Changed cases",
		"q3 \\| with pipe", // pipes in queries are escaped so the table survives
		"top_k=10, rerank, judge, alias=normal",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q:\n%s", want, md)
		}
	}
}

func TestCompareUseCaseRejectsDifferentDatasetsAndUnfinishedRuns(t *testing.T) {
	store := newMemEvalStore(eval.Dataset{ID: "ds", Name: "d"}, nil)
	_ = store.CreateRun(context.Background(), eval.Run{ID: "a", DatasetID: "ds", Status: eval.RunStatusDone})
	_ = store.CreateRun(context.Background(), eval.Run{ID: "b", DatasetID: "other", Status: eval.RunStatusDone})
	_ = store.CreateRun(context.Background(), eval.Run{ID: "c", DatasetID: "ds", Status: eval.RunStatusRunning})
	uc := usecase.NewCompareUseCase(store)

	if _, err := uc.Compare(context.Background(), "a", "b"); err == nil {
		t.Error("expected an error for runs from different datasets")
	}
	if _, err := uc.Compare(context.Background(), "a", "c"); err == nil {
		t.Error("expected an error when a run is not done")
	}
	if _, err := uc.Compare(context.Background(), "a", "a"); err == nil {
		t.Error("expected an error when comparing a run with itself")
	}
}
