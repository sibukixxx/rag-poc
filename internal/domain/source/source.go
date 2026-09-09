// Package source defines the provider-neutral contract for synchronizing
// external systems such as Slack, Chatwork, Jira, and cloud drives into a
// ForgeAI knowledge base.
package source

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var ErrNotFound = errors.New("source: not found")

type Connection struct {
	ID              string
	KnowledgeBaseID string
	Provider        string
	Name            string
	Config          json.RawMessage
	SecretName      string
	Cursor          string
	Enabled         bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type Item struct {
	ID              string
	ConnectionID    string
	ExternalID      string
	DocumentID      string
	Title           string
	SourceURL       string
	Metadata        json.RawMessage
	Visibility      json.RawMessage
	ContentHash     string
	RemoteUpdatedAt *time.Time
	SyncedAt        time.Time
	DeletedAt       *time.Time
}

type JobStatus string

const (
	JobStatusRunning   JobStatus = "running"
	JobStatusCompleted JobStatus = "completed"
	JobStatusFailed    JobStatus = "failed"
)

type SyncJob struct {
	ID           string
	ConnectionID string
	Status       JobStatus
	CursorBefore string
	CursorAfter  string
	Created      int
	Updated      int
	Deleted      int
	Skipped      int
	Error        string
	StartedAt    time.Time
	FinishedAt   *time.Time
}

// Document is the canonical representation returned by every connector.
// Body is already plain text; provider-specific parsing belongs in the
// connector, while chunking and embedding remain in the ingestion use case.
type Document struct {
	ExternalID string
	Title      string
	Body       string
	MimeType   string
	SourceURL  string
	Metadata   map[string]string
	Visibility []string
	UpdatedAt  *time.Time
	Deleted    bool
}

type Batch struct {
	Documents  []Document
	NextCursor string
	HasMore    bool
}

// Connector only retrieves and normalizes provider data. It must not write to
// ForgeAI storage directly, which keeps retry/cursor semantics consistent for
// every provider.
type Connector interface {
	Provider() string
	Pull(ctx context.Context, connection Connection, cursor string) (Batch, error)
}

type Registry interface {
	Get(provider string) (Connector, bool)
}

// Store owns synchronization state and atomically swaps the document mapped to
// an external item. Provider credentials are referenced by SecretName and must
// remain in ForgeAI's encrypted secret store.
type Store interface {
	CreateConnection(ctx context.Context, connection Connection) error
	GetConnection(ctx context.Context, id string) (*Connection, error)
	ListConnections(ctx context.Context, knowledgeBaseID string) ([]Connection, error)
	UpdateCursor(ctx context.Context, id, cursor string, updatedAt time.Time) error

	GetItem(ctx context.Context, connectionID, externalID string) (*Item, error)
	ReplaceItemDocument(ctx context.Context, item Item) error
	DeleteItemDocument(ctx context.Context, connectionID, externalID string, deletedAt time.Time) (bool, error)
	DeleteDocument(ctx context.Context, documentID string) error

	CreateJob(ctx context.Context, job SyncJob) error
	FinishJob(ctx context.Context, job SyncJob) error
	GetJob(ctx context.Context, id string) (*SyncJob, error)
}
