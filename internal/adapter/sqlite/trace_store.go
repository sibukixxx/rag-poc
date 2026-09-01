package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/sibukixxx/rag-poc/internal/domain/trace"
)

type TraceStore struct {
	db *sql.DB
}

var _ trace.Store = (*TraceStore)(nil)

func NewTraceStore(db *sql.DB) *TraceStore {
	return &TraceStore{db: db}
}

const timeLayout = time.RFC3339Nano

func (s *TraceStore) CreateTrace(ctx context.Context, t trace.Trace) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO traces (id, name, started_at, duration_ms, status, cost_usd)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			duration_ms = excluded.duration_ms,
			status = excluded.status,
			cost_usd = excluded.cost_usd
	`, t.ID, t.Name, t.StartedAt.Format(timeLayout), t.DurationMS, string(t.Status), t.CostUSD)
	if err != nil {
		return fmt.Errorf("storing trace %s: %w", t.ID, err)
	}
	return nil
}

func (s *TraceStore) CreateSpan(ctx context.Context, sp trace.Span) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO spans (
			id, trace_id, kind, name, started_at, duration_ms,
			model, input_tokens, output_tokens, cost_usd, status, error, input, output
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, sp.ID, sp.TraceID, string(sp.Kind), sp.Name, sp.StartedAt.Format(timeLayout), sp.DurationMS,
		sp.Model, sp.InputTokens, sp.OutputTokens, sp.CostUSD, string(sp.Status), sp.Error, sp.Input, sp.Output)
	if err != nil {
		return fmt.Errorf("storing span %s: %w", sp.ID, err)
	}
	return nil
}

func (s *TraceStore) GetTrace(ctx context.Context, id string) (*trace.Trace, []trace.Span, error) {
	var t trace.Trace
	var startedAt, status string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, started_at, duration_ms, status, cost_usd FROM traces WHERE id = ?`, id,
	).Scan(&t.ID, &t.Name, &startedAt, &t.DurationMS, &status, &t.CostUSD)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, fmt.Errorf("trace %s: %w", id, sql.ErrNoRows)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("loading trace %s: %w", id, err)
	}
	t.StartedAt, _ = time.Parse(timeLayout, startedAt)
	t.Status = trace.Status(status)

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, trace_id, kind, name, started_at, duration_ms,
		       model, input_tokens, output_tokens, cost_usd, status, error, input, output
		FROM spans WHERE trace_id = ? ORDER BY started_at ASC
	`, id)
	if err != nil {
		return nil, nil, fmt.Errorf("loading spans for trace %s: %w", id, err)
	}
	defer rows.Close()

	var spans []trace.Span
	for rows.Next() {
		var sp trace.Span
		var spStartedAt, spKind, spStatus string
		if err := rows.Scan(&sp.ID, &sp.TraceID, &spKind, &sp.Name, &spStartedAt, &sp.DurationMS,
			&sp.Model, &sp.InputTokens, &sp.OutputTokens, &sp.CostUSD, &spStatus, &sp.Error, &sp.Input, &sp.Output); err != nil {
			return nil, nil, fmt.Errorf("scanning span: %w", err)
		}
		sp.Kind = trace.SpanKind(spKind)
		sp.Status = trace.Status(spStatus)
		sp.StartedAt, _ = time.Parse(timeLayout, spStartedAt)
		spans = append(spans, sp)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	return &t, spans, nil
}

func (s *TraceStore) ListTraces(ctx context.Context, limit int) ([]trace.Trace, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, started_at, duration_ms, status, cost_usd FROM traces ORDER BY started_at DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("listing traces: %w", err)
	}
	defer rows.Close()

	var traces []trace.Trace
	for rows.Next() {
		var t trace.Trace
		var startedAt, status string
		if err := rows.Scan(&t.ID, &t.Name, &startedAt, &t.DurationMS, &status, &t.CostUSD); err != nil {
			return nil, fmt.Errorf("scanning trace: %w", err)
		}
		t.StartedAt, _ = time.Parse(timeLayout, startedAt)
		t.Status = trace.Status(status)
		traces = append(traces, t)
	}
	return traces, rows.Err()
}
