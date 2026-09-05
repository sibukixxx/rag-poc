// Package eval defines the Golden Dataset / Retrieval Evaluation domain:
// a Dataset of Cases (query + expected documents) scoped to one knowledge
// base, and Runs that score SearchUseCase's results against those
// expectations (docs/ROADMAP.md W7, docs/V0.1_SPEC.md §8).
//
// Cases and results reference documents by filename rather than document
// ID: re-ingesting a file creates a new document row (ingestion only
// dedupes chunk embeddings, not the document itself), so a dataset tied to
// document IDs would silently break the next time the corpus is
// re-ingested. Filenames survive that.
package eval

import (
	"context"
	"time"
)

type Dataset struct {
	ID              string
	Name            string
	KnowledgeBaseID string
	CreatedAt       time.Time
}

// Case is one golden question: a query, the filenames a good retrieval
// result should surface, and (optionally) a reference answer the LLM
// Judge scores Correctness against (W8). Without a reference answer the
// judge still scores Groundedness/Relevance, and scores Correctness by
// consistency with the retrieved context.
type Case struct {
	ID                string
	DatasetID         string
	Query             string
	ExpectedFilenames []string
	ExpectedAnswer    string
	CreatedAt         time.Time
}

type RunStatus string

const (
	RunStatusPending RunStatus = "pending"
	RunStatusRunning RunStatus = "running"
	RunStatusDone    RunStatus = "done"
	RunStatusFailed  RunStatus = "failed"
)

// Run is one execution of a Dataset's cases against a particular
// configuration: search (top_k, rerank) and, when Judge is set, answer
// generation (Alias) + LLM judging. Aggregate metrics are the mean of
// each case's own metric (docs/V0.1_SPEC.md §8).
type Run struct {
	ID           string
	DatasetID    string
	Status       RunStatus
	Error        string
	TopK         int
	Rerank       bool
	Judge        bool   // also generate a RAG answer per case and judge it
	Alias        string // LLM alias used to generate answers when Judge is set
	RecallAtK    float64
	PrecisionAtK float64
	MRR          float64
	HitRate      float64
	Correctness  float64 // judge metrics: mean over judged cases, 0.0-1.0
	Groundedness float64
	Relevance    float64
	CostUSD      float64 // total LLM cost of the run (answers + judgments)
	StartedAt    time.Time
	FinishedAt   *time.Time
}

// CaseResult is one case's outcome within a Run. The retrieval fields are
// always populated; the answer/judge fields only when the run had Judge
// set.
type CaseResult struct {
	ID                 string
	RunID              string
	CaseID             string
	RetrievedFilenames []string
	RecallAtK          float64
	PrecisionAtK       float64
	ReciprocalRank     float64
	Hit                bool
	Answer             string
	Correctness        float64
	Groundedness       float64
	Relevance          float64
	JudgeReason        string
	JudgeModel         string
	JudgePromptVersion int
	CostUSD            float64
	DurationMS         int64
	Error              string
}

// Store persists datasets, their cases, and evaluation runs/results.
type Store interface {
	// EnsureDataset returns the dataset with the given name, creating it
	// (scoped to knowledgeBaseID) if it doesn't exist yet — the same
	// idempotent-POST pattern as knowledge.Store.EnsureKnowledgeBase.
	EnsureDataset(ctx context.Context, name, knowledgeBaseID string) (*Dataset, error)
	GetDataset(ctx context.Context, id string) (*Dataset, error)
	GetDatasetByName(ctx context.Context, name string) (*Dataset, error)
	ListDatasets(ctx context.Context) ([]Dataset, error)

	AddCases(ctx context.Context, datasetID string, cases []Case) ([]Case, error)
	ListCases(ctx context.Context, datasetID string) ([]Case, error)

	CreateRun(ctx context.Context, run Run) error
	// UpdateRun overwrites a run's mutable fields (status/error/metrics/
	// finished_at); it is called once to mark "running" and once more to
	// finalize as "done"/"failed".
	UpdateRun(ctx context.Context, run Run) error
	GetRun(ctx context.Context, id string) (*Run, error)
	ListRuns(ctx context.Context, datasetID string) ([]Run, error)

	CreateCaseResults(ctx context.Context, results []CaseResult) error
	ListCaseResults(ctx context.Context, runID string) ([]CaseResult, error)
}
