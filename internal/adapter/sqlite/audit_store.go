package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"

	"github.com/sibukixxx/rag-poc/internal/domain/audit"
)

// AuditStore persists the security audit trail in audit_events.
type AuditStore struct {
	db *sql.DB
}

var _ audit.Store = (*AuditStore)(nil)

func NewAuditStore(db *sql.DB) *AuditStore {
	return &AuditStore{db: db}
}

func (s *AuditStore) Record(ctx context.Context, e audit.Event) error {
	if e.ID == "" {
		e.ID = uuid.NewString()
	}
	if e.OccurredAt.IsZero() {
		e.OccurredAt = time.Now()
	}
	meta := e.Metadata
	if meta == nil {
		meta = map[string]string{}
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("encoding audit metadata: %w", err)
	}
	// Audit writes must outlive a cancelled request (e.g. a client that
	// disconnects right after a rejected token), so they do not inherit
	// the request's cancellation.
	_, err = s.db.ExecContext(context.WithoutCancel(ctx), `
		INSERT INTO audit_events (id, occurred_at, action, outcome, actor, target, metadata_json)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.OccurredAt.UTC().Format(timeLayout), e.Action, string(e.Outcome), e.Actor, e.Target, string(metaJSON))
	if err != nil {
		log.Printf("audit: recording %s failed: %v", e.Action, err)
		return fmt.Errorf("recording audit event %s: %w", e.Action, err)
	}
	return nil
}

func (s *AuditStore) ListEvents(ctx context.Context, limit int) ([]audit.Event, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, occurred_at, action, outcome, actor, target, metadata_json
		FROM audit_events ORDER BY occurred_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("listing audit events: %w", err)
	}
	defer rows.Close()
	var events []audit.Event
	for rows.Next() {
		var e audit.Event
		var occurredAt, outcome, metaJSON string
		if err := rows.Scan(&e.ID, &occurredAt, &e.Action, &outcome, &e.Actor, &e.Target, &metaJSON); err != nil {
			return nil, fmt.Errorf("scanning audit event: %w", err)
		}
		e.OccurredAt, _ = time.Parse(timeLayout, occurredAt)
		e.Outcome = audit.Outcome(outcome)
		if err := json.Unmarshal([]byte(metaJSON), &e.Metadata); err != nil {
			return nil, fmt.Errorf("decoding audit metadata: %w", err)
		}
		if len(e.Metadata) == 0 {
			e.Metadata = nil
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

func (s *AuditStore) DeleteEventsBefore(ctx context.Context, cutoff time.Time) (int, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM audit_events WHERE occurred_at < ?`, cutoff.UTC().Format(timeLayout))
	if err != nil {
		return 0, fmt.Errorf("deleting expired audit events: %w", err)
	}
	n, err := res.RowsAffected()
	return int(n), err
}
