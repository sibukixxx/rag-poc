package sqlite_test

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/sibukixxx/rag-poc/internal/adapter/sqlite"
	"github.com/sibukixxx/rag-poc/internal/domain/audit"
	"github.com/sibukixxx/rag-poc/internal/domain/trace"
)

func TestAuditStoreRecordsAndListsNewestFirst(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "forgeai.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := sqlite.NewAuditStore(db)
	ctx := context.Background()
	older := audit.Event{
		ID: "e1", OccurredAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), Action: audit.ActionRuntimeTokenIssue,
		Outcome: audit.OutcomeSuccess, Actor: "http:127.0.0.1", Target: "deployment:dep-1",
		Metadata: map[string]string{"token_id": "tok-1", "token_fingerprint": "abc123"},
	}
	newer := audit.Event{
		ID: "e2", OccurredAt: time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC), Action: audit.ActionRuntimeAuthRejected,
		Outcome: audit.OutcomeDenied, Actor: "runtime", Target: "deployment_slug:app",
	}
	for _, e := range []audit.Event{older, newer} {
		if err := store.Record(ctx, e); err != nil {
			t.Fatal(err)
		}
	}

	got, err := store.ListEvents(ctx, 10)

	if err != nil {
		t.Fatal(err)
	}
	if want := []audit.Event{newer, older}; !reflect.DeepEqual(want, got) {
		t.Fatalf("events:\nwant %+v\ngot  %+v", want, got)
	}
}

func TestAuditStoreRetentionDeletesOnlyOldAuditEvents(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "forgeai.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	store := sqlite.NewAuditStore(db)
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Record(ctx, audit.Event{ID: "old", OccurredAt: old, Action: audit.ActionServerStart, Outcome: audit.OutcomeSuccess, Actor: "system"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Record(ctx, audit.Event{ID: "new", OccurredAt: old.AddDate(1, 0, 0), Action: audit.ActionServerStart, Outcome: audit.OutcomeSuccess, Actor: "system"}); err != nil {
		t.Fatal(err)
	}
	// An ordinary trace older than the cutoff must survive audit retention.
	if err := sqlite.NewTraceStore(db).CreateTrace(ctx, trace.Trace{ID: "tr", Name: "chat", StartedAt: old, Status: trace.StatusOK}); err != nil {
		t.Fatal(err)
	}

	n, err := store.DeleteEventsBefore(ctx, old.AddDate(0, 6, 0))

	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("deleted audit events = %d, want 1", n)
	}
	if got := countRows(t, db, `SELECT COUNT(1) FROM audit_events`); got != 1 {
		t.Fatalf("audit events left = %d, want 1", got)
	}
	if got := countRows(t, db, `SELECT COUNT(1) FROM traces`); got != 1 {
		t.Fatalf("audit retention deleted ordinary traces: %d left, want 1", got)
	}
}
