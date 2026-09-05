package usecase

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/sibukixxx/rag-poc/internal/domain/eval"
	"github.com/sibukixxx/rag-poc/internal/domain/retrieval"
	"github.com/sibukixxx/rag-poc/internal/domain/trace"
)

// defaultEvalTopK matches SearchUseCase's own default, so an evaluation run
// with no explicit top_k scores retrieval the same way a real user's
// default search would.
const defaultEvalTopK = 10

// EvaluationUseCase runs a Dataset's Cases through Hybrid Search and scores
// the results against each case's expected filenames: Recall@K,
// Precision@K, MRR, and Hit Rate (docs/V0.1_SPEC.md §8, docs/ROADMAP.md W7).
// Judge-based answer-quality scoring (Correctness/Groundedness/Relevance)
// is W8's addition on top of the same Run/CaseResult shape.
type EvaluationUseCase struct {
	Search   *SearchUseCase
	Datasets eval.Store
	Traces   trace.Store
}

func NewEvaluationUseCase(search *SearchUseCase, datasets eval.Store, traces trace.Store) *EvaluationUseCase {
	return &EvaluationUseCase{Search: search, Datasets: datasets, Traces: traces}
}

// CreateRun records a pending run for a dataset; Execute does the actual
// work. Splitting the two lets an HTTP caller return the run (with an ID
// to poll) immediately while Execute runs in the background
// (docs/V0.1_SPEC.md §6: "run 開始（非同期）").
func (u *EvaluationUseCase) CreateRun(ctx context.Context, datasetID string, topK int, rerank bool) (*eval.Run, error) {
	if topK <= 0 {
		topK = defaultEvalTopK
	}
	run := eval.Run{
		ID: uuid.NewString(), DatasetID: datasetID, Status: eval.RunStatusPending,
		TopK: topK, Rerank: rerank, StartedAt: time.Now(),
	}
	if err := u.Datasets.CreateRun(ctx, run); err != nil {
		return nil, fmt.Errorf("creating evaluation run: %w", err)
	}
	return &run, nil
}

// Execute scores every case in run's dataset and finalizes the run as
// done/failed. A case that fails to search (e.g. embedding API error) is
// recorded with its own error and scored as a miss rather than aborting
// the whole run — one bad question shouldn't hide the other 49 questions'
// results.
func (u *EvaluationUseCase) Execute(ctx context.Context, runID string) error {
	run, err := u.Datasets.GetRun(ctx, runID)
	if err != nil {
		return fmt.Errorf("loading run %s: %w", runID, err)
	}

	dataset, err := u.Datasets.GetDataset(ctx, run.DatasetID)
	if err != nil {
		wrapped := fmt.Errorf("loading dataset %s: %w", run.DatasetID, err)
		u.failRun(ctx, *run, wrapped, "")
		return wrapped
	}

	cases, err := u.Datasets.ListCases(ctx, dataset.ID)
	if err != nil {
		wrapped := fmt.Errorf("loading cases for dataset %s: %w", dataset.Name, err)
		u.failRun(ctx, *run, wrapped, dataset.Name)
		return wrapped
	}
	if len(cases) == 0 {
		wrapped := fmt.Errorf("dataset %s has no cases", dataset.Name)
		u.failRun(ctx, *run, wrapped, dataset.Name)
		return wrapped
	}

	run.Status = eval.RunStatusRunning
	if err := u.Datasets.UpdateRun(ctx, *run); err != nil {
		return fmt.Errorf("marking run %s running: %w", runID, err)
	}

	results := make([]eval.CaseResult, 0, len(cases))
	var sumRecall, sumPrecision, sumMRR, sumHit float64
	for _, c := range cases {
		cr := u.scoreCase(ctx, run.ID, dataset.KnowledgeBaseID, run.TopK, run.Rerank, c)
		results = append(results, cr)
		sumRecall += cr.RecallAtK
		sumPrecision += cr.PrecisionAtK
		sumMRR += cr.ReciprocalRank
		if cr.Hit {
			sumHit++
		}
	}

	if err := u.Datasets.CreateCaseResults(ctx, results); err != nil {
		wrapped := fmt.Errorf("saving case results for run %s: %w", runID, err)
		u.failRun(ctx, *run, wrapped, dataset.Name)
		return wrapped
	}

	n := float64(len(cases))
	finished := time.Now()
	run.Status = eval.RunStatusDone
	run.RecallAtK = sumRecall / n
	run.PrecisionAtK = sumPrecision / n
	run.MRR = sumMRR / n
	run.HitRate = sumHit / n
	run.FinishedAt = &finished
	if err := u.Datasets.UpdateRun(ctx, *run); err != nil {
		return fmt.Errorf("finalizing run %s: %w", runID, err)
	}

	u.recordTrace(*run, dataset.Name)
	return nil
}

// scoreCase runs one case's query through Search and compares the
// (filename-deduplicated) retrieval order against the case's expected
// filenames.
func (u *EvaluationUseCase) scoreCase(ctx context.Context, runID, knowledgeBaseID string, topK int, rerank bool, c eval.Case) eval.CaseResult {
	cr := eval.CaseResult{RunID: runID, CaseID: c.ID}

	results, err := u.Search.Search(ctx, knowledgeBaseID, c.Query, retrieval.Options{TopK: topK, Rerank: rerank})
	if err != nil {
		cr.Error = err.Error()
		return cr
	}

	retrieved := uniqueFilenames(results)
	cr.RetrievedFilenames = retrieved

	expected := make(map[string]bool, len(c.ExpectedFilenames))
	for _, f := range c.ExpectedFilenames {
		expected[f] = true
	}

	var hits int
	for rank, f := range retrieved {
		if !expected[f] {
			continue
		}
		hits++
		if cr.ReciprocalRank == 0 {
			cr.ReciprocalRank = 1.0 / float64(rank+1)
		}
	}
	if len(expected) > 0 {
		cr.RecallAtK = float64(hits) / float64(len(expected))
	}
	if len(retrieved) > 0 {
		cr.PrecisionAtK = float64(hits) / float64(len(retrieved))
	}
	cr.Hit = hits > 0
	return cr
}

// uniqueFilenames collapses per-chunk results to their first-occurrence
// filename order, since Recall/Precision/MRR/Hit Rate score at the
// document level, not the chunk level (a document with 3 matching chunks
// shouldn't count 3x, and shouldn't crowd out the top_k budget).
func uniqueFilenames(results []retrieval.Result) []string {
	seen := make(map[string]bool, len(results))
	out := make([]string, 0, len(results))
	for _, r := range results {
		if seen[r.Filename] {
			continue
		}
		seen[r.Filename] = true
		out = append(out, r.Filename)
	}
	return out
}

func (u *EvaluationUseCase) failRun(ctx context.Context, run eval.Run, err error, datasetName string) {
	finished := time.Now()
	run.Status = eval.RunStatusFailed
	run.Error = err.Error()
	run.FinishedAt = &finished
	_ = u.Datasets.UpdateRun(ctx, run)
	u.recordTrace(run, datasetName)
}

func (u *EvaluationUseCase) recordTrace(run eval.Run, datasetName string) {
	if u.Traces == nil {
		return
	}
	status := trace.StatusOK
	if run.Status == eval.RunStatusFailed {
		status = trace.StatusError
	}
	var duration int64
	if run.FinishedAt != nil {
		duration = run.FinishedAt.Sub(run.StartedAt).Milliseconds()
	}
	name := "eval_run"
	if datasetName != "" {
		name = fmt.Sprintf("eval_run:%s", datasetName)
	}
	_ = u.Traces.CreateTrace(context.Background(), trace.Trace{
		ID: run.ID, Name: name, StartedAt: run.StartedAt, DurationMS: duration, Status: status,
	})
}
