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

// defaultEvalAlias is the answering alias for judge runs when none is
// given — the same default the Playground and RAG chat use.
const defaultEvalAlias = "normal"

// EvaluationUseCase runs a Dataset's Cases through Hybrid Search and scores
// the results against each case's expected filenames: Recall@K,
// Precision@K, MRR, and Hit Rate (docs/V0.1_SPEC.md §8, docs/ROADMAP.md W7).
// With RunOptions.Judge set it also generates a RAG answer per case and has
// the LLM Judge score it for Correctness / Groundedness / Relevance (W8).
type EvaluationUseCase struct {
	Search   *SearchUseCase
	RAG      *RAGChatUseCase // required for judge runs; may be nil otherwise
	Judge    *LLMJudge       // required for judge runs; may be nil otherwise
	Datasets eval.Store
	Traces   trace.Store
}

func NewEvaluationUseCase(search *SearchUseCase, rag *RAGChatUseCase, judge *LLMJudge, datasets eval.Store, traces trace.Store) *EvaluationUseCase {
	return &EvaluationUseCase{Search: search, RAG: rag, Judge: judge, Datasets: datasets, Traces: traces}
}

// RunOptions is the configuration snapshot one run is scored under.
type RunOptions struct {
	TopK   int
	Rerank bool
	Judge  bool   // also generate an answer per case and judge it
	Alias  string // answering alias for judge runs (default "normal")
}

// CreateRun records a pending run for a dataset; Execute does the actual
// work. Splitting the two lets an HTTP caller return the run (with an ID
// to poll) immediately while Execute runs in the background
// (docs/V0.1_SPEC.md §6: "run 開始（非同期）").
func (u *EvaluationUseCase) CreateRun(ctx context.Context, datasetID string, opts RunOptions) (*eval.Run, error) {
	if opts.TopK <= 0 {
		opts.TopK = defaultEvalTopK
	}
	if opts.Judge {
		if u.RAG == nil || u.Judge == nil {
			return nil, fmt.Errorf("judge runs require an answering pipeline and a judge to be configured")
		}
		if opts.Alias == "" {
			opts.Alias = defaultEvalAlias
		}
	} else {
		opts.Alias = ""
	}
	run := eval.Run{
		ID: uuid.NewString(), DatasetID: datasetID, Status: eval.RunStatusPending,
		TopK: opts.TopK, Rerank: opts.Rerank, Judge: opts.Judge, Alias: opts.Alias,
		StartedAt: time.Now(),
	}
	if err := u.Datasets.CreateRun(ctx, run); err != nil {
		return nil, fmt.Errorf("creating evaluation run: %w", err)
	}
	return &run, nil
}

// Execute scores every case in run's dataset and finalizes the run as
// done/failed. A case that fails to search, answer, or be judged (e.g. an
// API error) is recorded with its own error and scored as a miss rather
// than aborting the whole run — one bad question shouldn't hide the other
// 49 questions' results.
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
	if run.Judge && (u.RAG == nil || u.Judge == nil) {
		wrapped := fmt.Errorf("judge runs require an answering pipeline and a judge to be configured")
		u.failRun(ctx, *run, wrapped, dataset.Name)
		return wrapped
	}

	run.Status = eval.RunStatusRunning
	if err := u.Datasets.UpdateRun(ctx, *run); err != nil {
		return fmt.Errorf("marking run %s running: %w", runID, err)
	}

	results := make([]eval.CaseResult, 0, len(cases))
	var sum struct{ recall, precision, mrr, hit, correctness, groundedness, relevance, cost float64 }
	for _, c := range cases {
		cr := u.scoreCase(ctx, *run, dataset.KnowledgeBaseID, c)
		results = append(results, cr)
		sum.recall += cr.RecallAtK
		sum.precision += cr.PrecisionAtK
		sum.mrr += cr.ReciprocalRank
		if cr.Hit {
			sum.hit++
		}
		sum.correctness += cr.Correctness
		sum.groundedness += cr.Groundedness
		sum.relevance += cr.Relevance
		sum.cost += cr.CostUSD
	}

	if err := u.Datasets.CreateCaseResults(ctx, results); err != nil {
		wrapped := fmt.Errorf("saving case results for run %s: %w", runID, err)
		u.failRun(ctx, *run, wrapped, dataset.Name)
		return wrapped
	}

	n := float64(len(cases))
	finished := time.Now()
	run.Status = eval.RunStatusDone
	run.RecallAtK = sum.recall / n
	run.PrecisionAtK = sum.precision / n
	run.MRR = sum.mrr / n
	run.HitRate = sum.hit / n
	if run.Judge {
		run.Correctness = sum.correctness / n
		run.Groundedness = sum.groundedness / n
		run.Relevance = sum.relevance / n
	}
	run.CostUSD = sum.cost
	run.FinishedAt = &finished
	if err := u.Datasets.UpdateRun(ctx, *run); err != nil {
		return fmt.Errorf("finalizing run %s: %w", runID, err)
	}

	u.recordTrace(*run, dataset.Name)
	return nil
}

// scoreCase runs one case's query through Search and compares the
// (filename-deduplicated) retrieval order against the case's expected
// filenames. For judge runs it then generates an answer through the RAG
// pipeline (its own retrieval, exactly as a real question would get) and
// has the judge grade it against the case's reference answer.
func (u *EvaluationUseCase) scoreCase(ctx context.Context, run eval.Run, knowledgeBaseID string, c eval.Case) eval.CaseResult {
	start := time.Now()
	cr := eval.CaseResult{RunID: run.ID, CaseID: c.ID}
	defer func() { cr.DurationMS = time.Since(start).Milliseconds() }()

	results, err := u.Search.Search(ctx, knowledgeBaseID, c.Query, retrieval.Options{TopK: run.TopK, Rerank: run.Rerank})
	if err != nil {
		cr.Error = fmt.Sprintf("search: %v", err)
		return cr
	}
	scoreRetrieval(&cr, results, c.ExpectedFilenames)

	if !run.Judge {
		return cr
	}

	answer, err := u.RAG.Answer(ctx, knowledgeBaseID, run.Alias, c.Query, run.Rerank)
	if err != nil {
		cr.Error = fmt.Sprintf("answer: %v", err)
		return cr
	}
	cr.Answer = answer.Content
	cr.CostUSD += answer.CostUSD

	verdict, err := u.Judge.Judge(ctx, JudgeInput{
		Question: c.Query, Context: answer.Context, Answer: answer.Content, ReferenceAnswer: c.ExpectedAnswer,
	}, "")
	if err != nil {
		cr.Error = fmt.Sprintf("judge: %v", err)
		return cr
	}
	cr.Correctness = verdict.Correctness
	cr.Groundedness = verdict.Groundedness
	cr.Relevance = verdict.Relevance
	cr.JudgeReason = verdict.Reason
	cr.JudgeModel = verdict.Model
	cr.JudgePromptVersion = verdict.PromptVersion
	cr.CostUSD += verdict.CostUSD
	return cr
}

// scoreRetrieval fills cr's retrieval metrics from results vs. expected.
func scoreRetrieval(cr *eval.CaseResult, results []retrieval.Result, expectedFilenames []string) {
	retrieved := uniqueFilenames(results)
	cr.RetrievedFilenames = retrieved

	expected := make(map[string]bool, len(expectedFilenames))
	for _, f := range expectedFilenames {
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
		ID: run.ID, Name: name, StartedAt: run.StartedAt, DurationMS: duration, Status: status, CostUSD: run.CostUSD,
	})
}
