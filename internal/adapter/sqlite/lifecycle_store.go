package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/sibukixxx/rag-poc/internal/domain/lifecycle"
)

// LifecycleStore implements customer-data deletion and retention. Foreign
// keys cascade most derived rows, but the FTS5 virtual table and
// source_items (ON DELETE SET NULL) are removed explicitly here, and the
// report is computed before deletion so operators see what was purged.
type LifecycleStore struct {
	db *sql.DB
}

var _ lifecycle.Store = (*LifecycleStore)(nil)

func NewLifecycleStore(db *sql.DB) *LifecycleStore {
	return &LifecycleStore{db: db}
}

// documentScope selects every document of one knowledge base or one
// document, expressed as a subquery usable in all the count/delete
// statements below.
type documentScope struct {
	where string
	arg   string
}

func (s documentScope) docIDs() string {
	return `SELECT id FROM documents WHERE ` + s.where
}

func (s documentScope) countDerived(ctx context.Context, tx *sql.Tx, r *lifecycle.DeletionReport) error {
	counts := []struct {
		dst   *int
		query string
	}{
		{&r.Documents, `SELECT COUNT(1) FROM documents WHERE ` + s.where},
		{&r.Chunks, `SELECT COUNT(1) FROM chunks WHERE document_id IN (` + s.docIDs() + `)`},
		{&r.Embeddings, `SELECT COUNT(1) FROM embeddings WHERE chunk_id IN (SELECT id FROM chunks WHERE document_id IN (` + s.docIDs() + `))`},
		{&r.FTSRows, `SELECT COUNT(1) FROM chunks_fts WHERE document_id IN (` + s.docIDs() + `)`},
	}
	for _, c := range counts {
		if err := tx.QueryRowContext(ctx, c.query, s.arg).Scan(c.dst); err != nil {
			return fmt.Errorf("counting derived rows: %w", err)
		}
	}
	return nil
}

func (s documentScope) deleteUncascaded(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM chunks_fts WHERE document_id IN (`+s.docIDs()+`)`, s.arg); err != nil {
		return fmt.Errorf("deleting FTS rows: %w", err)
	}
	return nil
}

func (l *LifecycleStore) DeleteDocument(ctx context.Context, documentID string) (lifecycle.DeletionReport, error) {
	var report lifecycle.DeletionReport
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return report, fmt.Errorf("beginning document deletion: %w", err)
	}
	defer tx.Rollback()

	scope := documentScope{where: `id = ?`, arg: documentID}
	if err := scope.countDerived(ctx, tx, &report); err != nil {
		return lifecycle.DeletionReport{}, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM source_items WHERE document_id = ?`, documentID).Scan(&report.SourceItems); err != nil {
		return lifecycle.DeletionReport{}, fmt.Errorf("counting source items: %w", err)
	}
	if err := scope.deleteUncascaded(ctx, tx); err != nil {
		return lifecycle.DeletionReport{}, err
	}
	// source_items keep title/URL metadata and would survive with a NULL
	// document_id; a purge removes them so a later sync re-creates them
	// only if the upstream object still exists.
	if _, err := tx.ExecContext(ctx, `DELETE FROM source_items WHERE document_id = ?`, documentID); err != nil {
		return lifecycle.DeletionReport{}, fmt.Errorf("deleting source items: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM documents WHERE id = ?`, documentID); err != nil {
		return lifecycle.DeletionReport{}, fmt.Errorf("deleting document: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return lifecycle.DeletionReport{}, fmt.Errorf("committing document deletion: %w", err)
	}
	return report, nil
}

func (l *LifecycleStore) DeleteKnowledgeBase(ctx context.Context, knowledgeBaseID string, includeDeployments bool) (lifecycle.DeletionReport, error) {
	var report lifecycle.DeletionReport
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return report, fmt.Errorf("beginning knowledge-base deletion: %w", err)
	}
	defer tx.Rollback()

	if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM knowledge_bases WHERE id = ?`, knowledgeBaseID).Scan(&report.KnowledgeBases); err != nil {
		return lifecycle.DeletionReport{}, fmt.Errorf("looking up knowledge base: %w", err)
	}
	if report.KnowledgeBases == 0 {
		return lifecycle.DeletionReport{}, nil
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM deployments WHERE knowledge_base_id = ?`, knowledgeBaseID).Scan(&report.Deployments); err != nil {
		return lifecycle.DeletionReport{}, fmt.Errorf("counting deployments: %w", err)
	}
	if report.Deployments > 0 && !includeDeployments {
		return lifecycle.DeletionReport{}, lifecycle.ErrKnowledgeBaseHasDeployments
	}

	scope := documentScope{where: `knowledge_base_id = ?`, arg: knowledgeBaseID}
	if err := scope.countDerived(ctx, tx, &report); err != nil {
		return lifecycle.DeletionReport{}, err
	}
	kbCounts := []struct {
		dst   *int
		query string
	}{
		{&report.SourceConnections, `SELECT COUNT(1) FROM source_connections WHERE knowledge_base_id = ?`},
		{&report.SourceItems, `SELECT COUNT(1) FROM source_items WHERE connection_id IN (SELECT id FROM source_connections WHERE knowledge_base_id = ?)`},
		{&report.Datasets, `SELECT COUNT(1) FROM datasets WHERE knowledge_base_id = ?`},
		{&report.EvaluationRuns, `SELECT COUNT(1) FROM evaluation_runs WHERE dataset_id IN (SELECT id FROM datasets WHERE knowledge_base_id = ?)`},
	}
	for _, c := range kbCounts {
		if err := tx.QueryRowContext(ctx, c.query, knowledgeBaseID).Scan(c.dst); err != nil {
			return lifecycle.DeletionReport{}, fmt.Errorf("counting knowledge-base artifacts: %w", err)
		}
	}

	if err := scope.deleteUncascaded(ctx, tx); err != nil {
		return lifecycle.DeletionReport{}, err
	}
	// deployments reference the KB with ON DELETE RESTRICT; runtime tokens
	// cascade from deployments. Everything else cascades from the KB row.
	if _, err := tx.ExecContext(ctx, `DELETE FROM deployments WHERE knowledge_base_id = ?`, knowledgeBaseID); err != nil {
		return lifecycle.DeletionReport{}, fmt.Errorf("deleting deployments: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM knowledge_bases WHERE id = ?`, knowledgeBaseID); err != nil {
		return lifecycle.DeletionReport{}, fmt.Errorf("deleting knowledge base: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return lifecycle.DeletionReport{}, fmt.Errorf("committing knowledge-base deletion: %w", err)
	}
	return report, nil
}

func (l *LifecycleStore) DeleteTracesStartedBefore(ctx context.Context, cutoff time.Time) (int, error) {
	res, err := l.db.ExecContext(ctx, `DELETE FROM traces WHERE started_at < ?`, cutoff.UTC().Format(timeLayout))
	if err != nil {
		return 0, fmt.Errorf("deleting expired traces: %w", err)
	}
	n, err := res.RowsAffected()
	return int(n), err
}

func (l *LifecycleStore) DeleteEvaluationRunsStartedBefore(ctx context.Context, cutoff time.Time) (int, error) {
	res, err := l.db.ExecContext(ctx, `DELETE FROM evaluation_runs WHERE started_at < ?`, cutoff.UTC().Format(timeLayout))
	if err != nil {
		return 0, fmt.Errorf("deleting expired evaluation runs: %w", err)
	}
	n, err := res.RowsAffected()
	return int(n), err
}

func (l *LifecycleStore) Compact(ctx context.Context) error {
	// FTS5 only tombstones deleted rows; merging segments drops their terms.
	if _, err := l.db.ExecContext(ctx, `INSERT INTO chunks_fts(chunks_fts) VALUES('optimize')`); err != nil {
		return fmt.Errorf("optimizing FTS index: %w", err)
	}
	if _, err := l.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return fmt.Errorf("checkpointing WAL: %w", err)
	}
	if _, err := l.db.ExecContext(ctx, `VACUUM`); err != nil {
		return fmt.Errorf("vacuuming database: %w", err)
	}
	if _, err := l.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return fmt.Errorf("checkpointing WAL after vacuum: %w", err)
	}
	return nil
}
