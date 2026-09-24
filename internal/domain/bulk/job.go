package bulk

import (
	"context"
	"errors"
	"time"
)

var ErrNotFound = errors.New("bulk: not found")

// JobStatus is the lifecycle of one ingestion job. *_requested states are
// set by operators and honoured by the runner between files.
type JobStatus string

const (
	JobQueued          JobStatus = "queued"
	JobRunning         JobStatus = "running"
	JobPauseRequested  JobStatus = "pause_requested"
	JobPaused          JobStatus = "paused"
	JobCancelRequested JobStatus = "cancel_requested"
	JobCancelled       JobStatus = "cancelled"
	JobCompleted       JobStatus = "completed"
	JobFailed          JobStatus = "failed"
)

// Finished reports whether the job will not run again.
func (s JobStatus) Finished() bool {
	return s == JobCancelled || s == JobCompleted || s == JobFailed
}

// ItemStatus is the state of one file within a job.
type ItemStatus string

const (
	ItemPending    ItemStatus = "pending"
	ItemProcessing ItemStatus = "processing"
	ItemCompleted  ItemStatus = "completed"
	ItemSkipped    ItemStatus = "skipped"
	ItemFailed     ItemStatus = "failed"
)

// Job is one durable bulk-ingestion run over a source connection.
type Job struct {
	ID            string      `json:"id"`
	ConnectionID  string      `json:"connection_id"`
	Status        JobStatus   `json:"status"`
	DiscoveryDone bool        `json:"discovery_done"`
	Scan          ScanSummary `json:"scan"`
	Deleted       int         `json:"deleted"`
	LastError     string      `json:"last_error,omitempty"`
	CreatedAt     time.Time   `json:"created_at"`
	UpdatedAt     time.Time   `json:"updated_at"`
	StartedAt     *time.Time  `json:"started_at,omitempty"`
	FinishedAt    *time.Time  `json:"finished_at,omitempty"`
}

// Counts summarise a job's files by state.
type Counts struct {
	Discovered int `json:"discovered"`
	Pending    int `json:"pending"`
	Processing int `json:"processing"`
	Completed  int `json:"completed"`
	Skipped    int `json:"skipped"`
	Failed     int `json:"failed"`
}

// Item is one file of a job.
type Item struct {
	JobID    string     `json:"-"`
	Path     string     `json:"path"`
	Size     int64      `json:"size"`
	ModTime  int64      `json:"mod_time"`
	Status   ItemStatus `json:"status"`
	Attempts int        `json:"attempts"`
	Error    string     `json:"error,omitempty"`
}

// Store persists jobs and per-file progress so a job survives restarts.
type Store interface {
	CreateJob(ctx context.Context, job Job) error
	GetJob(ctx context.Context, id string) (*Job, error)
	ListJobs(ctx context.Context, connectionID string, limit int) ([]Job, error)
	ListUnfinishedJobs(ctx context.Context) ([]Job, error)
	// SetJobStatus records a runner-side transition.
	SetJobStatus(ctx context.Context, id string, status JobStatus, lastError string, at time.Time) error
	// RequestJobStatus moves a job to `to` only from one of `from`, so an
	// operator request cannot overwrite a job that already finished.
	RequestJobStatus(ctx context.Context, id string, from []JobStatus, to JobStatus, at time.Time) (bool, error)
	SaveScan(ctx context.Context, id string, summary ScanSummary, done bool, at time.Time) error
	AddDeleted(ctx context.Context, id string, n int) error

	// AddItems inserts discovered files; files already known to the job keep
	// their state, so re-running discovery after a restart is harmless.
	AddItems(ctx context.Context, jobID string, items []Item) error
	// ResetProcessing returns files interrupted mid-processing to pending.
	ResetProcessing(ctx context.Context, jobID string) error
	// ClaimPending atomically marks up to limit pending files as processing.
	ClaimPending(ctx context.Context, jobID string, limit int) ([]Item, error)
	FinishItem(ctx context.Context, jobID, path string, status ItemStatus, attempts int, errMsg string) error
	Counts(ctx context.Context, jobID string) (Counts, error)
	ListItems(ctx context.Context, jobID string, status ItemStatus, limit int) ([]Item, error)
	// UnseenSourceItems lists live source items of the connection whose
	// external ID the job did not discover: files removed from the corpus.
	UnseenSourceItems(ctx context.Context, jobID, connectionID string) ([]string, error)
}
