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
	// AuthState is the credential lifecycle for OAuth-style connectors.
	AuthState AuthState
	// LastError is the most recent operator-actionable failure.
	LastError string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// AuthState describes whether a connection's credentials are usable.
type AuthState string

const (
	// AuthNotRequired: the connector does not use OAuth (filesystem, feeds).
	AuthNotRequired AuthState = "not_required"
	// AuthAuthorizationRequired: created, but no one has consented yet.
	AuthAuthorizationRequired AuthState = "authorization_required"
	// AuthAuthorized: a grant is stored in the Secret Store.
	AuthAuthorized AuthState = "authorized"
	// AuthReauthorizationRequired: the grant was revoked or expired.
	AuthReauthorizationRequired AuthState = "reauthorization_required"
)

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

// Credentials yields a currently valid access token for a connection.
type Credentials interface {
	AccessToken(ctx context.Context) (string, error)
}

// CredentialedConnector is implemented by connectors that call an API on
// behalf of an authorized account. The sync use case supplies credentials;
// connectors never read the Secret Store or config_json for tokens.
type CredentialedConnector interface {
	Connector
	PullWithCredentials(ctx context.Context, connection Connection, cursor string, creds Credentials) (Batch, error)
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
	UpdateConnectionAuth(ctx context.Context, id string, state AuthState, secretName, lastError string, at time.Time) error
	SetConnectionEnabled(ctx context.Context, id string, enabled bool, at time.Time) error
	// DeleteConnection removes the connection, its items and jobs, and every
	// document it synced; it returns how many documents were removed.
	DeleteConnection(ctx context.Context, id string) (int, error)
	LatestJob(ctx context.Context, connectionID string) (*SyncJob, error)

	GetItem(ctx context.Context, connectionID, externalID string) (*Item, error)
	ReplaceItemDocument(ctx context.Context, item Item) error
	DeleteItemDocument(ctx context.Context, connectionID, externalID string, deletedAt time.Time) (bool, error)
	DeleteDocument(ctx context.Context, documentID string) error

	CreateJob(ctx context.Context, job SyncJob) error
	FinishJob(ctx context.Context, job SyncJob) error
	GetJob(ctx context.Context, id string) (*SyncJob, error)
}
