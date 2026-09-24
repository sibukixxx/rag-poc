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
	if c.AuthState == "" {
		c.AuthState = source.AuthNotRequired
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
		(id, knowledge_base_id, provider, name, config_json, secret_name, cursor, enabled, auth_state, last_error, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, c.ID, c.KnowledgeBaseID, c.Provider, c.Name, string(c.Config), c.SecretName, c.Cursor, c.Enabled,
		string(c.AuthState), c.LastError, now.Format(timeLayout), updated.Format(timeLayout))
	if err != nil {
		return fmt.Errorf("creating source connection %s: %w", c.ID, err)
	}
	return nil
}

const connectionColumns = `id, knowledge_base_id, provider, name, config_json, secret_name, cursor, enabled,
	auth_state, last_error, created_at, updated_at`

func scanConnection(row rowScanner) (*source.Connection, error) {
	var c source.Connection
	var config, authState, createdAt, updatedAt string
	var enabled int
	if err := row.Scan(&c.ID, &c.KnowledgeBaseID, &c.Provider, &c.Name, &config, &c.SecretName, &c.Cursor, &enabled,
		&authState, &c.LastError, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	c.Config = []byte(config)
	c.Enabled = enabled != 0
	c.AuthState = source.AuthState(authState)
	c.CreatedAt, _ = time.Parse(timeLayout, createdAt)
	c.UpdatedAt, _ = time.Parse(timeLayout, updatedAt)
	return &c, nil
}

func (s *SourceStore) GetConnection(ctx context.Context, id string) (*source.Connection, error) {
	c, err := scanConnection(s.db.QueryRowContext(ctx, `SELECT `+connectionColumns+` FROM source_connections WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, source.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("loading source connection %s: %w", id, err)
	}
	return c, nil
}

func (s *SourceStore) ListConnections(ctx context.Context, knowledgeBaseID string) ([]source.Connection, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+connectionColumns+` FROM source_connections
		WHERE knowledge_base_id = ? ORDER BY created_at DESC`, knowledgeBaseID)
	if err != nil {
		return nil, fmt.Errorf("listing source connections: %w", err)
	}
	defer rows.Close()
	var out []source.Connection
	for rows.Next() {
		c, err := scanConnection(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning source connection: %w", err)
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

func (s *SourceStore) UpdateConnectionAuth(ctx context.Context, id string, state source.AuthState, secretName, lastError string, at time.Time) error {
	res, err := s.db.ExecContext(ctx, `UPDATE source_connections SET auth_state = ?, secret_name = ?, last_error = ?, updated_at = ? WHERE id = ?`,
		string(state), secretName, lastError, at.UTC().Format(timeLayout), id)
	if err != nil {
		return fmt.Errorf("updating source connection auth %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return source.ErrNotFound
	}
	return nil
}

func (s *SourceStore) SetConnectionEnabled(ctx context.Context, id string, enabled bool, at time.Time) error {
	res, err := s.db.ExecContext(ctx, `UPDATE source_connections SET enabled = ?, updated_at = ? WHERE id = ?`,
		enabled, at.UTC().Format(timeLayout), id)
	if err != nil {
		return fmt.Errorf("updating source connection %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return source.ErrNotFound
	}
	return nil
}

func (s *SourceStore) DeleteConnection(ctx context.Context, id string) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT document_id FROM source_items WHERE connection_id = ? AND document_id IS NOT NULL`, id)
	if err != nil {
		return 0, fmt.Errorf("listing connection documents: %w", err)
	}
	var docIDs []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			rows.Close()
			return 0, err
		}
		docIDs = append(docIDs, d)
	}
	rows.Close()
	for _, d := range docIDs {
		if err := deleteSourceDocument(ctx, tx, d); err != nil {
			return 0, err
		}
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM source_connections WHERE id = ?`, id)
	if err != nil {
		return 0, fmt.Errorf("deleting source connection %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, source.ErrNotFound
	}
	return len(docIDs), tx.Commit()
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

const syncJobColumns = `id, connection_id, status, cursor_before, cursor_after, created_count, updated_count,
	deleted_count, skipped_count, error, started_at, finished_at`

func scanSyncJob(row rowScanner) (*source.SyncJob, error) {
	var job source.SyncJob
	var status, startedAt string
	var finishedAt sql.NullString
	if err := row.Scan(&job.ID, &job.ConnectionID, &status, &job.CursorBefore, &job.CursorAfter, &job.Created,
		&job.Updated, &job.Deleted, &job.Skipped, &job.Error, &startedAt, &finishedAt); err != nil {
		return nil, err
	}
	job.Status = source.JobStatus(status)
	job.StartedAt, _ = time.Parse(timeLayout, startedAt)
	job.FinishedAt = parseOptionalTime(finishedAt)
	return &job, nil
}

func (s *SourceStore) GetJob(ctx context.Context, id string) (*source.SyncJob, error) {
	job, err := scanSyncJob(s.db.QueryRowContext(ctx, `SELECT `+syncJobColumns+` FROM source_sync_jobs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, source.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("loading source sync job %s: %w", id, err)
	}
	return job, nil
}

func (s *SourceStore) LatestJob(ctx context.Context, connectionID string) (*source.SyncJob, error) {
	job, err := scanSyncJob(s.db.QueryRowContext(ctx, `SELECT `+syncJobColumns+` FROM source_sync_jobs
		WHERE connection_id = ? ORDER BY started_at DESC LIMIT 1`, connectionID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, source.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("loading latest sync job: %w", err)
	}
	return job, nil
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
