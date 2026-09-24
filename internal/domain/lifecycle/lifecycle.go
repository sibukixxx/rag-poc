// Package lifecycle defines customer-data deletion and retention for the
// v0.1 storage model (#23). docs/security/DATA_LIFECYCLE.md is the
// human-readable inventory; this package is its executable contract.
package lifecycle

import (
	"context"
	"errors"
	"time"
)

// ErrKnowledgeBaseHasDeployments is returned when deleting a knowledge base
// would silently take a live Runtime deployment offline. The operator must
// opt in to deleting those deployments as well.
var ErrKnowledgeBaseHasDeployments = errors.New("lifecycle: knowledge base still has deployments")

// DeletionReport counts what a deletion removed. Deleting something that is
// already gone is not an error; it returns a zero report, so purge commands
// can be re-run safely.
type DeletionReport struct {
	KnowledgeBases    int `json:"knowledge_bases"`
	Documents         int `json:"documents"`
	Chunks            int `json:"chunks"`
	Embeddings        int `json:"embeddings"`
	FTSRows           int `json:"fts_rows"`
	SourceConnections int `json:"source_connections"`
	SourceItems       int `json:"source_items"`
	Datasets          int `json:"datasets"`
	EvaluationRuns    int `json:"evaluation_runs"`
	Deployments       int `json:"deployments"`
}

// Store deletes customer-derived data and every artifact derived from it.
type Store interface {
	DeleteDocument(ctx context.Context, documentID string) (DeletionReport, error)
	DeleteKnowledgeBase(ctx context.Context, knowledgeBaseID string, includeDeployments bool) (DeletionReport, error)
	DeleteTracesStartedBefore(ctx context.Context, cutoff time.Time) (int, error)
	DeleteEvaluationRunsStartedBefore(ctx context.Context, cutoff time.Time) (int, error)
	// Compact checkpoints the write-ahead log and rebuilds the database file
	// so deleted pages are not left in the files ForgeAI manages.
	Compact(ctx context.Context) error
}
