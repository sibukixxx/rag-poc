package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/sibukixxx/rag-poc/internal/domain/eval"
)

type EvalStore struct {
	db *sql.DB
}

var _ eval.Store = (*EvalStore)(nil)

func NewEvalStore(db *sql.DB) *EvalStore {
	return &EvalStore{db: db}
}

func (s *EvalStore) EnsureDataset(ctx context.Context, name, knowledgeBaseID string) (*eval.Dataset, error) {
	if d, err := s.GetDatasetByName(ctx, name); err == nil {
		return d, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	d := eval.Dataset{ID: uuid.NewString(), Name: name, KnowledgeBaseID: knowledgeBaseID, CreatedAt: time.Now()}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO datasets (id, name, knowledge_base_id, created_at) VALUES (?, ?, ?, ?)`,
		d.ID, d.Name, d.KnowledgeBaseID, d.CreatedAt.Format(timeLayout),
	)
	if err != nil {
		return nil, fmt.Errorf("creating dataset %s: %w", name, err)
	}
	return &d, nil
}

func (s *EvalStore) GetDataset(ctx context.Context, id string) (*eval.Dataset, error) {
	return s.scanDatasetRow(s.db.QueryRowContext(ctx,
		`SELECT id, name, knowledge_base_id, created_at FROM datasets WHERE id = ?`, id))
}

func (s *EvalStore) GetDatasetByName(ctx context.Context, name string) (*eval.Dataset, error) {
	return s.scanDatasetRow(s.db.QueryRowContext(ctx,
		`SELECT id, name, knowledge_base_id, created_at FROM datasets WHERE name = ?`, name))
}

func (s *EvalStore) scanDatasetRow(row *sql.Row) (*eval.Dataset, error) {
	var d eval.Dataset
	var createdAt string
	if err := row.Scan(&d.ID, &d.Name, &d.KnowledgeBaseID, &createdAt); err != nil {
		return nil, err
	}
	d.CreatedAt, _ = time.Parse(timeLayout, createdAt)
	return &d, nil
}

func (s *EvalStore) ListDatasets(ctx context.Context) ([]eval.Dataset, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, knowledge_base_id, created_at FROM datasets ORDER BY name ASC`)
	if err != nil {
		return nil, fmt.Errorf("listing datasets: %w", err)
	}
	defer rows.Close()

	var out []eval.Dataset
	for rows.Next() {
		var d eval.Dataset
		var createdAt string
		if err := rows.Scan(&d.ID, &d.Name, &d.KnowledgeBaseID, &createdAt); err != nil {
			return nil, fmt.Errorf("scanning dataset: %w", err)
		}
		d.CreatedAt, _ = time.Parse(timeLayout, createdAt)
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *EvalStore) AddCases(ctx context.Context, datasetID string, cases []eval.Case) ([]eval.Case, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("beginning AddCases transaction: %w", err)
	}
	defer tx.Rollback()

	out := make([]eval.Case, len(cases))
	for i, c := range cases {
		expected, err := json.Marshal(c.ExpectedFilenames)
		if err != nil {
			return nil, fmt.Errorf("encoding expected_filenames: %w", err)
		}
		c.ID = uuid.NewString()
		c.DatasetID = datasetID
		c.CreatedAt = time.Now()
		_, err = tx.ExecContext(ctx, `
			INSERT INTO dataset_cases (id, dataset_id, query, expected_filenames, expected_answer, created_at)
			VALUES (?, ?, ?, ?, ?, ?)
		`, c.ID, c.DatasetID, c.Query, string(expected), c.ExpectedAnswer, c.CreatedAt.Format(timeLayout))
		if err != nil {
			return nil, fmt.Errorf("inserting case %d: %w", i, err)
		}
		out[i] = c
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing AddCases: %w", err)
	}
	return out, nil
}

func (s *EvalStore) ListCases(ctx context.Context, datasetID string) ([]eval.Case, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, dataset_id, query, expected_filenames, expected_answer, created_at
		FROM dataset_cases WHERE dataset_id = ? ORDER BY created_at ASC
	`, datasetID)
	if err != nil {
		return nil, fmt.Errorf("listing cases for dataset %s: %w", datasetID, err)
	}
	defer rows.Close()

	var out []eval.Case
	for rows.Next() {
		var c eval.Case
		var createdAt, expected string
		if err := rows.Scan(&c.ID, &c.DatasetID, &c.Query, &expected, &c.ExpectedAnswer, &createdAt); err != nil {
			return nil, fmt.Errorf("scanning case: %w", err)
		}
		c.CreatedAt, _ = time.Parse(timeLayout, createdAt)
		_ = json.Unmarshal([]byte(expected), &c.ExpectedFilenames)
		out = append(out, c)
	}
	return out, rows.Err()
}

const runColumns = `id, dataset_id, status, error, top_k, rerank, judge, alias,
	recall_at_k, precision_at_k, mrr, hit_rate,
	correctness, groundedness, relevance, cost_usd, started_at, finished_at`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanRun(row rowScanner) (*eval.Run, error) {
	var run eval.Run
	var status, startedAt string
	var finishedAt sql.NullString
	var rerank, judge int
	err := row.Scan(&run.ID, &run.DatasetID, &status, &run.Error, &run.TopK, &rerank, &judge, &run.Alias,
		&run.RecallAtK, &run.PrecisionAtK, &run.MRR, &run.HitRate,
		&run.Correctness, &run.Groundedness, &run.Relevance, &run.CostUSD, &startedAt, &finishedAt)
	if err != nil {
		return nil, err
	}
	run.Status = eval.RunStatus(status)
	run.Rerank = rerank != 0
	run.Judge = judge != 0
	run.StartedAt, _ = time.Parse(timeLayout, startedAt)
	if finishedAt.Valid {
		t, _ := time.Parse(timeLayout, finishedAt.String)
		run.FinishedAt = &t
	}
	return &run, nil
}

func (s *EvalStore) CreateRun(ctx context.Context, run eval.Run) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO evaluation_runs (`+runColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, run.ID, run.DatasetID, string(run.Status), run.Error, run.TopK, boolToInt(run.Rerank), boolToInt(run.Judge), run.Alias,
		run.RecallAtK, run.PrecisionAtK, run.MRR, run.HitRate,
		run.Correctness, run.Groundedness, run.Relevance, run.CostUSD,
		run.StartedAt.Format(timeLayout), formatNullableTime(run.FinishedAt))
	if err != nil {
		return fmt.Errorf("creating evaluation run %s: %w", run.ID, err)
	}
	return nil
}

func (s *EvalStore) UpdateRun(ctx context.Context, run eval.Run) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE evaluation_runs SET
			status = ?, error = ?, recall_at_k = ?, precision_at_k = ?, mrr = ?, hit_rate = ?,
			correctness = ?, groundedness = ?, relevance = ?, cost_usd = ?, finished_at = ?
		WHERE id = ?
	`, string(run.Status), run.Error, run.RecallAtK, run.PrecisionAtK, run.MRR, run.HitRate,
		run.Correctness, run.Groundedness, run.Relevance, run.CostUSD,
		formatNullableTime(run.FinishedAt), run.ID)
	if err != nil {
		return fmt.Errorf("updating evaluation run %s: %w", run.ID, err)
	}
	return nil
}

func (s *EvalStore) GetRun(ctx context.Context, id string) (*eval.Run, error) {
	run, err := scanRun(s.db.QueryRowContext(ctx, `SELECT `+runColumns+` FROM evaluation_runs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("evaluation run %s: %w", id, sql.ErrNoRows)
	}
	if err != nil {
		return nil, fmt.Errorf("loading evaluation run %s: %w", id, err)
	}
	return run, nil
}

func (s *EvalStore) ListRuns(ctx context.Context, datasetID string) ([]eval.Run, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+runColumns+` FROM evaluation_runs WHERE dataset_id = ? ORDER BY started_at DESC`, datasetID)
	if err != nil {
		return nil, fmt.Errorf("listing evaluation runs for dataset %s: %w", datasetID, err)
	}
	defer rows.Close()

	var out []eval.Run
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning evaluation run: %w", err)
		}
		out = append(out, *run)
	}
	return out, rows.Err()
}

func (s *EvalStore) CreateCaseResults(ctx context.Context, results []eval.CaseResult) error {
	if len(results) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning CreateCaseResults transaction: %w", err)
	}
	defer tx.Rollback()

	for i, r := range results {
		retrieved, err := json.Marshal(r.RetrievedFilenames)
		if err != nil {
			return fmt.Errorf("encoding retrieved_filenames: %w", err)
		}
		if r.ID == "" {
			r.ID = uuid.NewString()
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO evaluation_results (
				id, run_id, case_id, retrieved_filenames,
				recall_at_k, precision_at_k, reciprocal_rank, hit,
				answer, correctness, groundedness, relevance, judge_reason,
				judge_model, judge_prompt_version, cost_usd, duration_ms, error
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, r.ID, r.RunID, r.CaseID, string(retrieved),
			r.RecallAtK, r.PrecisionAtK, r.ReciprocalRank, boolToInt(r.Hit),
			r.Answer, r.Correctness, r.Groundedness, r.Relevance, r.JudgeReason,
			r.JudgeModel, r.JudgePromptVersion, r.CostUSD, r.DurationMS, r.Error)
		if err != nil {
			return fmt.Errorf("inserting case result %d: %w", i, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing CreateCaseResults: %w", err)
	}
	return nil
}

func (s *EvalStore) ListCaseResults(ctx context.Context, runID string) ([]eval.CaseResult, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, run_id, case_id, retrieved_filenames,
		       recall_at_k, precision_at_k, reciprocal_rank, hit,
		       answer, correctness, groundedness, relevance, judge_reason,
		       judge_model, judge_prompt_version, cost_usd, duration_ms, error
		FROM evaluation_results WHERE run_id = ?
	`, runID)
	if err != nil {
		return nil, fmt.Errorf("listing case results for run %s: %w", runID, err)
	}
	defer rows.Close()

	var out []eval.CaseResult
	for rows.Next() {
		var r eval.CaseResult
		var retrieved string
		var hit int
		if err := rows.Scan(&r.ID, &r.RunID, &r.CaseID, &retrieved,
			&r.RecallAtK, &r.PrecisionAtK, &r.ReciprocalRank, &hit,
			&r.Answer, &r.Correctness, &r.Groundedness, &r.Relevance, &r.JudgeReason,
			&r.JudgeModel, &r.JudgePromptVersion, &r.CostUSD, &r.DurationMS, &r.Error); err != nil {
			return nil, fmt.Errorf("scanning case result: %w", err)
		}
		r.Hit = hit != 0
		_ = json.Unmarshal([]byte(retrieved), &r.RetrievedFilenames)
		out = append(out, r)
	}
	return out, rows.Err()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func formatNullableTime(t *time.Time) sql.NullString {
	if t == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: t.Format(timeLayout), Valid: true}
}
