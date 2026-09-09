package usecase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sibukixxx/rag-poc/internal/domain/knowledge"
	"github.com/sibukixxx/rag-poc/internal/domain/source"
)

const maxSourceDocumentBytes = 32 << 20

type SourceTextIngester interface {
	IngestText(ctx context.Context, knowledgeBaseID, title, mimeType, text string) (*knowledge.Document, error)
}

type SourceSyncUseCase struct {
	Store      source.Store
	Registry   source.Registry
	Ingester   SourceTextIngester
	Now        func() time.Time
	NewID      func() string
	MaxBatches int
}

func NewSourceSyncUseCase(store source.Store, registry source.Registry, ingester SourceTextIngester) *SourceSyncUseCase {
	return &SourceSyncUseCase{
		Store: store, Registry: registry, Ingester: ingester,
		Now: time.Now, NewID: uuid.NewString, MaxBatches: 1000,
	}
}

// Sync pulls complete batches from one connector and advances the durable
// cursor only after every item in a batch has been committed. A retry therefore
// replays at most one partial batch, while content hashes prevent duplicate
// embeddings and external IDs prevent duplicate source items.
func (u *SourceSyncUseCase) Sync(ctx context.Context, connectionID string) (*source.SyncJob, error) {
	connection, err := u.Store.GetConnection(ctx, connectionID)
	if err != nil {
		return nil, err
	}
	if !connection.Enabled {
		return nil, fmt.Errorf("source connection %s is disabled", connectionID)
	}
	connector, ok := u.Registry.Get(connection.Provider)
	if !ok {
		return nil, fmt.Errorf("source provider %q is not registered", connection.Provider)
	}

	job := source.SyncJob{
		ID: u.NewID(), ConnectionID: connection.ID, Status: source.JobStatusRunning,
		CursorBefore: connection.Cursor, CursorAfter: connection.Cursor, StartedAt: u.Now(),
	}
	if err := u.Store.CreateJob(ctx, job); err != nil {
		return nil, err
	}
	fail := func(syncErr error) (*source.SyncJob, error) {
		finished := u.Now()
		job.Status = source.JobStatusFailed
		job.Error = syncErr.Error()
		job.FinishedAt = &finished
		_ = u.Store.FinishJob(context.Background(), job)
		return &job, syncErr
	}

	cursor := connection.Cursor
	for batchNumber := 0; ; batchNumber++ {
		if batchNumber >= u.MaxBatches {
			return fail(fmt.Errorf("source sync exceeded %d batches", u.MaxBatches))
		}
		batch, err := connector.Pull(ctx, *connection, cursor)
		if err != nil {
			return fail(fmt.Errorf("pulling %s source: %w", connection.Provider, err))
		}
		for _, external := range batch.Documents {
			if err := u.applyDocument(ctx, *connection, external, &job); err != nil {
				return fail(err)
			}
		}
		if batch.HasMore && batch.NextCursor == cursor {
			return fail(errors.New("source connector returned has_more without advancing its cursor"))
		}
		if batch.NextCursor != cursor {
			if err := u.Store.UpdateCursor(ctx, connection.ID, batch.NextCursor, u.Now()); err != nil {
				return fail(err)
			}
			cursor = batch.NextCursor
			job.CursorAfter = cursor
		}
		if !batch.HasMore {
			break
		}
	}

	finished := u.Now()
	job.Status = source.JobStatusCompleted
	job.FinishedAt = &finished
	if err := u.Store.FinishJob(ctx, job); err != nil {
		return nil, err
	}
	return &job, nil
}

func (u *SourceSyncUseCase) applyDocument(ctx context.Context, connection source.Connection, external source.Document, job *source.SyncJob) error {
	external.ExternalID = strings.TrimSpace(external.ExternalID)
	if external.ExternalID == "" {
		return errors.New("source connector returned a document without external_id")
	}
	if external.Deleted {
		deleted, err := u.Store.DeleteItemDocument(ctx, connection.ID, external.ExternalID, u.Now())
		if err != nil {
			return fmt.Errorf("deleting external item %s: %w", external.ExternalID, err)
		}
		if deleted {
			job.Deleted++
		} else {
			job.Skipped++
		}
		return nil
	}
	if strings.TrimSpace(external.Title) == "" {
		external.Title = connection.Provider + "-" + external.ExternalID
	}
	if strings.TrimSpace(external.Body) == "" {
		return fmt.Errorf("external item %s has no text", external.ExternalID)
	}
	if len([]byte(external.Body)) > maxSourceDocumentBytes {
		return fmt.Errorf("external item %s exceeds %d bytes", external.ExternalID, maxSourceDocumentBytes)
	}

	hash, metadata, visibility, err := sourceDocumentHash(external)
	if err != nil {
		return fmt.Errorf("hashing external item %s: %w", external.ExternalID, err)
	}
	existing, err := u.Store.GetItem(ctx, connection.ID, external.ExternalID)
	if err != nil && !errors.Is(err, source.ErrNotFound) {
		return err
	}
	now := u.Now()
	if err == nil && existing.DeletedAt == nil && existing.ContentHash == hash && existing.DocumentID != "" {
		existing.Title = external.Title
		existing.SourceURL = external.SourceURL
		existing.Metadata = metadata
		existing.Visibility = visibility
		existing.RemoteUpdatedAt = external.UpdatedAt
		existing.SyncedAt = now
		if err := u.Store.ReplaceItemDocument(ctx, *existing); err != nil {
			return err
		}
		job.Skipped++
		return nil
	}

	document, err := u.Ingester.IngestText(ctx, connection.KnowledgeBaseID, external.Title, external.MimeType, external.Body)
	if err != nil {
		return fmt.Errorf("ingesting external item %s: %w", external.ExternalID, err)
	}
	itemID := u.NewID()
	if existing != nil {
		itemID = existing.ID
	}
	item := source.Item{
		ID: itemID, ConnectionID: connection.ID, ExternalID: external.ExternalID,
		DocumentID: document.ID, Title: external.Title, SourceURL: external.SourceURL,
		Metadata: metadata, Visibility: visibility, ContentHash: hash,
		RemoteUpdatedAt: external.UpdatedAt, SyncedAt: now,
	}
	if err := u.Store.ReplaceItemDocument(ctx, item); err != nil {
		_ = u.Store.DeleteDocument(context.Background(), document.ID)
		return fmt.Errorf("mapping external item %s: %w", external.ExternalID, err)
	}
	if existing == nil || existing.DeletedAt != nil || existing.DocumentID == "" {
		job.Created++
	} else {
		job.Updated++
	}
	return nil
}

func sourceDocumentHash(document source.Document) (string, json.RawMessage, json.RawMessage, error) {
	if document.Metadata == nil {
		document.Metadata = map[string]string{}
	}
	if document.Visibility == nil {
		document.Visibility = []string{}
	}
	metadata, err := json.Marshal(document.Metadata)
	if err != nil {
		return "", nil, nil, err
	}
	visibility, err := json.Marshal(document.Visibility)
	if err != nil {
		return "", nil, nil, err
	}
	payload, err := json.Marshal(struct {
		Title      string          `json:"title"`
		Body       string          `json:"body"`
		MimeType   string          `json:"mime_type"`
		SourceURL  string          `json:"source_url"`
		Metadata   json.RawMessage `json:"metadata"`
		Visibility json.RawMessage `json:"visibility"`
	}{document.Title, document.Body, document.MimeType, document.SourceURL, metadata, visibility})
	if err != nil {
		return "", nil, nil, err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), metadata, visibility, nil
}
