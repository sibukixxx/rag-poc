package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/sibukixxx/rag-poc/internal/domain/source"
)

type SourceStore struct {
	db *sql.DB
}

var _ source.Store = (*SourceStore)(nil)

func NewSourceStore(db *sql.DB) *SourceStore { return &SourceStore{db: db} }

func (s *SourceStore) CreateConnection(ctx context.Context, c source.Connection) error {
	if len(c.Config) == 0 {
		c.Config = []byte("{}")
	}
	now := c.CreatedAt
	if now.IsZero() {
		now = time.Now()
	}
	updated := c.UpdatedAt
	if updated.IsZero() {
		updated = now
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO source_connections
		(id, knowledge_base_id, provider, name, config_json, secret_name, cursor, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, c.ID, c.KnowledgeBaseID, c.Provider, c.Name, string(c.Config), c.SecretName, c.Cursor, c.Enabled,
		now.Format(timeLayout), updated.Format(timeLayout))
	if err != nil {
		return fmt.Errorf("creating source connection %s: %w", c.ID, err)
	}
	return nil
}

func (s *SourceStore) GetConnection(ctx context.Context, id string) (*source.Connection, error) {
	var c source.Connection
	var config, createdAt, updatedAt string
	var enabled int
	err := s.db.QueryRowContext(ctx, `
		SELECT id, knowledge_base_id, provider, name, config_json, secret_name, cursor, enabled, created_at, updated_at
		FROM source_connections WHERE id = ?
	`, id).Scan(&c.ID, &c.KnowledgeBaseID, &c.Provider, &c.Name, &config, &c.SecretName, &c.Cursor, &enabled, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, source.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("loading source connection %s: %w", id, err)
	}
	c.Config = []byte(config)
	c.Enabled = enabled != 0
	c.CreatedAt, _ = time.Parse(timeLayout, createdAt)
	c.UpdatedAt, _ = time.Parse(timeLayout, updatedAt)
	return &c, nil
}

func (s *SourceStore) ListConnections(ctx context.Context, knowledgeBaseID string) ([]source.Connection, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, knowledge_base_id, provider, name, config_json, secret_name, cursor, enabled, created_at, updated_at
		FROM source_connections WHERE knowledge_base_id = ? ORDER BY created_at DESC
	`, knowledgeBaseID)
	if err != nil {
		return nil, fmt.Errorf("listing source connections: %w", err)
	}
	defer rows.Close()
	var out []source.Connection
	for rows.Next() {
		var c source.Connection
		var config, createdAt, updatedAt string
		var enabled int
		if err := rows.Scan(&c.ID, &c.KnowledgeBaseID, &c.Provider, &c.Name, &config, &c.SecretName,
			&c.Cursor, &enabled, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scanning source connection: %w", err)
		}
		c.Config = []byte(config)
		c.Enabled = enabled != 0
		c.CreatedAt, _ = time.Parse(timeLayout, createdAt)
		c.UpdatedAt, _ = time.Parse(timeLayout, updatedAt)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *SourceStore) UpdateCursor(ctx context.Context, id, cursor string, updatedAt time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE source_connections SET cursor = ?, updated_at = ? WHERE id = ?`,
		cursor, updatedAt.Format(timeLayout), id)
	if err != nil {
		return fmt.Errorf("updating source cursor %s: %w", id, err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return source.ErrNotFound
	}
	return nil
}

func (s *SourceStore) GetItem(ctx context.Context, connectionID, externalID string) (*source.Item, error) {
	var item source.Item
	var documentID, remoteUpdatedAt, syncedAt, deletedAt sql.NullString
	var metadata, visibility string
	err := s.db.QueryRowContext(ctx, `
		SELECT id, connection_id, external_id, document_id, title, source_url, metadata_json, visibility_json, content_hash,
		       remote_updated_at, synced_at, deleted_at
		FROM source_items WHERE connection_id = ? AND external_id = ?
	`, connectionID, externalID).Scan(&item.ID, &item.ConnectionID, &item.ExternalID, &documentID, &item.Title,
		&item.SourceURL, &metadata, &visibility, &item.ContentHash, &remoteUpdatedAt, &syncedAt, &deletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, source.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("loading source item %s: %w", externalID, err)
	}
	item.DocumentID = documentID.String
	item.Metadata = []byte(metadata)
	item.Visibility = []byte(visibility)
	item.SyncedAt, _ = time.Parse(timeLayout, syncedAt.String)
	item.RemoteUpdatedAt = parseOptionalTime(remoteUpdatedAt)
	item.DeletedAt = parseOptionalTime(deletedAt)
	return &item, nil
}

// ReplaceItemDocument installs the newly ingested document and removes the old
// one in a single transaction. FTS5 rows are cleared explicitly because the
// standalone FTS table has no foreign key cascade.
func (s *SourceStore) ReplaceItemDocument(ctx context.Context, item source.Item) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning source item replacement: %w", err)
	}
	defer tx.Rollback()

	var oldDocumentID sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT document_id FROM source_items WHERE connection_id = ? AND external_id = ?`,
		item.ConnectionID, item.ExternalID).Scan(&oldDocumentID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("loading previous source item: %w", err)
	}
	var remoteUpdated any
	if item.RemoteUpdatedAt != nil {
		remoteUpdated = item.RemoteUpdatedAt.Format(timeLayout)
	}
	if len(item.Metadata) == 0 {
		item.Metadata = []byte("{}")
	}
	if len(item.Visibility) == 0 {
		item.Visibility = []byte("[]")
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO source_items
		(id, connection_id, external_id, document_id, title, source_url, metadata_json, visibility_json, content_hash, remote_updated_at, synced_at, deleted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)
		ON CONFLICT(connection_id, external_id) DO UPDATE SET
			document_id = excluded.document_id,
			title = excluded.title,
			source_url = excluded.source_url,
			metadata_json = excluded.metadata_json,
			visibility_json = excluded.visibility_json,
			content_hash = excluded.content_hash,
			remote_updated_at = excluded.remote_updated_at,
			synced_at = excluded.synced_at,
			deleted_at = NULL
	`, item.ID, item.ConnectionID, item.ExternalID, item.DocumentID, item.Title, item.SourceURL,
		string(item.Metadata), string(item.Visibility), item.ContentHash, remoteUpdated, item.SyncedAt.Format(timeLayout))
	if err != nil {
		return fmt.Errorf("upserting source item %s: %w", item.ExternalID, err)
	}
	if oldDocumentID.Valid && oldDocumentID.String != "" && oldDocumentID.String != item.DocumentID {
		if err := deleteSourceDocument(ctx, tx, oldDocumentID.String); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *SourceStore) DeleteItemDocument(ctx context.Context, connectionID, externalID string, deletedAt time.Time) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("beginning source item deletion: %w", err)
	}
	defer tx.Rollback()
	var documentID sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT document_id FROM source_items WHERE connection_id = ? AND external_id = ?`,
		connectionID, externalID).Scan(&documentID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("loading source item for deletion: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE source_items SET document_id = NULL, deleted_at = ?, synced_at = ? WHERE connection_id = ? AND external_id = ?`,
		deletedAt.Format(timeLayout), deletedAt.Format(timeLayout), connectionID, externalID); err != nil {
		return false, fmt.Errorf("marking source item deleted: %w", err)
	}
	if documentID.Valid && documentID.String != "" {
		if err := deleteSourceDocument(ctx, tx, documentID.String); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return documentID.Valid && documentID.String != "", nil
}

func (s *SourceStore) DeleteDocument(ctx context.Context, documentID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning source document deletion: %w", err)
	}
	defer tx.Rollback()
	if err := deleteSourceDocument(ctx, tx, documentID); err != nil {
		return err
	}
	return tx.Commit()
}

func deleteSourceDocument(ctx context.Context, tx *sql.Tx, documentID string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM chunks_fts WHERE document_id = ?`, documentID); err != nil {
		return fmt.Errorf("deleting source document FTS rows: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM documents WHERE id = ?`, documentID); err != nil {
		return fmt.Errorf("deleting source document: %w", err)
	}
	return nil
}

func (s *SourceStore) CreateJob(ctx context.Context, job source.SyncJob) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO source_sync_jobs
		(id, connection_id, status, cursor_before, cursor_after, created_count, updated_count,
		 deleted_count, skipped_count, error, started_at, finished_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)
	`, job.ID, job.ConnectionID, string(job.Status), job.CursorBefore, job.CursorAfter, job.Created,
		job.Updated, job.Deleted, job.Skipped, job.Error, job.StartedAt.Format(timeLayout))
	if err != nil {
		return fmt.Errorf("creating source sync job %s: %w", job.ID, err)
	}
	return nil
}

func (s *SourceStore) FinishJob(ctx context.Context, job source.SyncJob) error {
	var finished any
	if job.FinishedAt != nil {
		finished = job.FinishedAt.Format(timeLayout)
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE source_sync_jobs SET status = ?, cursor_after = ?, created_count = ?, updated_count = ?,
		deleted_count = ?, skipped_count = ?, error = ?, finished_at = ? WHERE id = ?
	`, string(job.Status), job.CursorAfter, job.Created, job.Updated, job.Deleted, job.Skipped, job.Error, finished, job.ID)
	if err != nil {
		return fmt.Errorf("finishing source sync job %s: %w", job.ID, err)
	}
	return nil
}

func (s *SourceStore) GetJob(ctx context.Context, id string) (*source.SyncJob, error) {
	var job source.SyncJob
	var status, startedAt string
	var finishedAt sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT id, connection_id, status, cursor_before, cursor_after, created_count, updated_count,
		       deleted_count, skipped_count, error, started_at, finished_at
		FROM source_sync_jobs WHERE id = ?
	`, id).Scan(&job.ID, &job.ConnectionID, &status, &job.CursorBefore, &job.CursorAfter, &job.Created,
		&job.Updated, &job.Deleted, &job.Skipped, &job.Error, &startedAt, &finishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, source.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("loading source sync job %s: %w", id, err)
	}
	job.Status = source.JobStatus(status)
	job.StartedAt, _ = time.Parse(timeLayout, startedAt)
	job.FinishedAt = parseOptionalTime(finishedAt)
	return &job, nil
}

func parseOptionalTime(value sql.NullString) *time.Time {
	if !value.Valid || value.String == "" {
		return nil
	}
	parsed, err := time.Parse(timeLayout, value.String)
	if err != nil {
		return nil
	}
	return &parsed
}
