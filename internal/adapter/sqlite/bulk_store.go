package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sibukixxx/rag-poc/internal/domain/bulk"
)

// BulkStore persists bulk-ingestion jobs and per-file progress (#30).
type BulkStore struct {
	db *sql.DB
}

var _ bulk.Store = (*BulkStore)(nil)

func NewBulkStore(db *sql.DB) *BulkStore {
	return &BulkStore{db: db}
}

const jobColumns = `id, connection_id, status, discovery_done, scan_json, deleted_count, last_error,
	created_at, updated_at, started_at, finished_at`

func (s *BulkStore) CreateJob(ctx context.Context, job bulk.Job) error {
	scan, err := json.Marshal(job.Scan)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO ingestion_jobs (id, connection_id, status, scan_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		job.ID, job.ConnectionID, string(job.Status), string(scan), job.CreatedAt.UTC().Format(timeLayout), job.UpdatedAt.UTC().Format(timeLayout))
	if err != nil {
		return fmt.Errorf("creating ingestion job: %w", err)
	}
	return nil
}

func scanJob(row rowScanner) (*bulk.Job, error) {
	var j bulk.Job
	var status, scanJSON, createdAt, updatedAt string
	var discoveryDone int
	var startedAt, finishedAt sql.NullString
	if err := row.Scan(&j.ID, &j.ConnectionID, &status, &discoveryDone, &scanJSON, &j.Deleted, &j.LastError,
		&createdAt, &updatedAt, &startedAt, &finishedAt); err != nil {
		return nil, err
	}
	j.Status = bulk.JobStatus(status)
	j.DiscoveryDone = discoveryDone != 0
	if err := json.Unmarshal([]byte(scanJSON), &j.Scan); err != nil {
		return nil, fmt.Errorf("decoding scan summary: %w", err)
	}
	j.CreatedAt, _ = time.Parse(timeLayout, createdAt)
	j.UpdatedAt, _ = time.Parse(timeLayout, updatedAt)
	j.StartedAt = parseOptionalTime(startedAt)
	j.FinishedAt = parseOptionalTime(finishedAt)
	return &j, nil
}

func (s *BulkStore) GetJob(ctx context.Context, id string) (*bulk.Job, error) {
	job, err := scanJob(s.db.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM ingestion_jobs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, bulk.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("loading ingestion job %s: %w", id, err)
	}
	return job, nil
}

func (s *BulkStore) queryJobs(ctx context.Context, query string, args ...any) ([]bulk.Job, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing ingestion jobs: %w", err)
	}
	defer rows.Close()
	var jobs []bulk.Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, *j)
	}
	return jobs, rows.Err()
}

func (s *BulkStore) ListJobs(ctx context.Context, connectionID string, limit int) ([]bulk.Job, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	return s.queryJobs(ctx, `SELECT `+jobColumns+` FROM ingestion_jobs WHERE connection_id = ? ORDER BY created_at DESC LIMIT ?`, connectionID, limit)
}

func (s *BulkStore) ListUnfinishedJobs(ctx context.Context) ([]bulk.Job, error) {
	return s.queryJobs(ctx, `SELECT `+jobColumns+` FROM ingestion_jobs
		WHERE status NOT IN (?, ?, ?, ?) ORDER BY created_at`,
		string(bulk.JobCompleted), string(bulk.JobFailed), string(bulk.JobCancelled), string(bulk.JobPaused))
}

func (s *BulkStore) SetJobStatus(ctx context.Context, id string, status bulk.JobStatus, lastError string, at time.Time) error {
	ts := at.UTC().Format(timeLayout)
	var finished any
	if status.Finished() {
		finished = ts
	}
	_, err := s.db.ExecContext(ctx, `UPDATE ingestion_jobs SET status = ?, last_error = ?, updated_at = ?,
		started_at = CASE WHEN ? = 'running' AND started_at IS NULL THEN ? ELSE started_at END,
		finished_at = COALESCE(?, finished_at)
		WHERE id = ?`, string(status), lastError, ts, string(status), ts, finished, id)
	if err != nil {
		return fmt.Errorf("updating ingestion job %s: %w", id, err)
	}
	return nil
}

func (s *BulkStore) RequestJobStatus(ctx context.Context, id string, from []bulk.JobStatus, to bulk.JobStatus, at time.Time) (bool, error) {
	if len(from) == 0 {
		return false, nil
	}
	args := []any{string(to), at.UTC().Format(timeLayout), id}
	for _, f := range from {
		args = append(args, string(f))
	}
	res, err := s.db.ExecContext(ctx, `UPDATE ingestion_jobs SET status = ?, updated_at = ?
		WHERE id = ? AND status IN (?`+strings.Repeat(",?", len(from)-1)+`)`, args...)
	if err != nil {
		return false, fmt.Errorf("requesting ingestion job status: %w", err)
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (s *BulkStore) SaveScan(ctx context.Context, id string, summary bulk.ScanSummary, done bool, at time.Time) error {
	scan, err := json.Marshal(summary)
	if err != nil {
		return err
	}
	doneInt := 0
	if done {
		doneInt = 1
	}
	_, err = s.db.ExecContext(ctx, `UPDATE ingestion_jobs SET scan_json = ?, discovery_done = ?, updated_at = ? WHERE id = ?`,
		string(scan), doneInt, at.UTC().Format(timeLayout), id)
	return err
}

func (s *BulkStore) AddDeleted(ctx context.Context, id string, n int) error {
	_, err := s.db.ExecContext(ctx, `UPDATE ingestion_jobs SET deleted_count = deleted_count + ? WHERE id = ?`, n, id)
	return err
}

func (s *BulkStore) AddItems(ctx context.Context, jobID string, items []bulk.Item) error {
	if len(items) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO ingestion_job_items (job_id, path, size, mod_time, status, updated_at)
		VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(job_id, path) DO NOTHING`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	now := time.Now().UTC().Format(timeLayout)
	for _, it := range items {
		if _, err := stmt.ExecContext(ctx, jobID, it.Path, it.Size, it.ModTime, string(bulk.ItemPending), now); err != nil {
			return fmt.Errorf("recording discovered file %s: %w", it.Path, err)
		}
	}
	return tx.Commit()
}

func (s *BulkStore) ResetProcessing(ctx context.Context, jobID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE ingestion_job_items SET status = ? WHERE job_id = ? AND status = ?`,
		string(bulk.ItemPending), jobID, string(bulk.ItemProcessing))
	return err
}

func (s *BulkStore) ClaimPending(ctx context.Context, jobID string, limit int) ([]bulk.Item, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT path, size, mod_time, attempts FROM ingestion_job_items
		WHERE job_id = ? AND status = ? ORDER BY path LIMIT ?`, jobID, string(bulk.ItemPending), limit)
	if err != nil {
		return nil, err
	}
	var items []bulk.Item
	for rows.Next() {
		it := bulk.Item{JobID: jobID, Status: bulk.ItemProcessing}
		if err := rows.Scan(&it.Path, &it.Size, &it.ModTime, &it.Attempts); err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(timeLayout)
	for _, it := range items {
		if _, err := tx.ExecContext(ctx, `UPDATE ingestion_job_items SET status = ?, updated_at = ? WHERE job_id = ? AND path = ?`,
			string(bulk.ItemProcessing), now, jobID, it.Path); err != nil {
			return nil, err
		}
	}
	return items, tx.Commit()
}

func (s *BulkStore) FinishItem(ctx context.Context, jobID, path string, status bulk.ItemStatus, attempts int, errMsg string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE ingestion_job_items SET status = ?, attempts = ?, error = ?, updated_at = ?
		WHERE job_id = ? AND path = ?`, string(status), attempts, errMsg, time.Now().UTC().Format(timeLayout), jobID, path)
	if err != nil {
		return fmt.Errorf("recording file %s: %w", path, err)
	}
	return nil
}

func (s *BulkStore) Counts(ctx context.Context, jobID string) (bulk.Counts, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT status, COUNT(1) FROM ingestion_job_items WHERE job_id = ? GROUP BY status`, jobID)
	if err != nil {
		return bulk.Counts{}, err
	}
	defer rows.Close()
	var c bulk.Counts
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return bulk.Counts{}, err
		}
		c.Discovered += n
		switch bulk.ItemStatus(status) {
		case bulk.ItemPending:
			c.Pending = n
		case bulk.ItemProcessing:
			c.Processing = n
		case bulk.ItemCompleted:
			c.Completed = n
		case bulk.ItemSkipped:
			c.Skipped = n
		case bulk.ItemFailed:
			c.Failed = n
		}
	}
	return c, rows.Err()
}

func (s *BulkStore) ListItems(ctx context.Context, jobID string, status bulk.ItemStatus, limit int) ([]bulk.Item, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT path, size, mod_time, status, attempts, error FROM ingestion_job_items
		WHERE job_id = ? AND status = ? ORDER BY path LIMIT ?`, jobID, string(status), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []bulk.Item
	for rows.Next() {
		it := bulk.Item{JobID: jobID}
		var st string
		if err := rows.Scan(&it.Path, &it.Size, &it.ModTime, &st, &it.Attempts, &it.Error); err != nil {
			return nil, err
		}
		it.Status = bulk.ItemStatus(st)
		items = append(items, it)
	}
	return items, rows.Err()
}

func (s *BulkStore) UnseenSourceItems(ctx context.Context, jobID, connectionID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT external_id FROM source_items
		WHERE connection_id = ? AND deleted_at IS NULL
		  AND external_id NOT IN (SELECT path FROM ingestion_job_items WHERE job_id = ?)
		ORDER BY external_id`, connectionID, jobID)
	if err != nil {
		return nil, fmt.Errorf("listing removed files: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
