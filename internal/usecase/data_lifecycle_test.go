package usecase

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/sibukixxx/rag-poc/internal/domain/lifecycle"
)

// recordingLifecycleStore is a fake that records retention cutoffs; the
// deletion SQL itself is covered by the sqlite LifecycleStore tests.
type recordingLifecycleStore struct {
	traceCutoffs []time.Time
	runCutoffs   []time.Time
}

func (s *recordingLifecycleStore) DeleteDocument(context.Context, string) (lifecycle.DeletionReport, error) {
	return lifecycle.DeletionReport{Documents: 1}, nil
}

func (s *recordingLifecycleStore) DeleteKnowledgeBase(context.Context, string, bool) (lifecycle.DeletionReport, error) {
	return lifecycle.DeletionReport{KnowledgeBases: 1}, nil
}

func (s *recordingLifecycleStore) DeleteTracesStartedBefore(_ context.Context, cutoff time.Time) (int, error) {
	s.traceCutoffs = append(s.traceCutoffs, cutoff)
	return 3, nil
}

func (s *recordingLifecycleStore) DeleteEvaluationRunsStartedBefore(_ context.Context, cutoff time.Time) (int, error) {
	s.runCutoffs = append(s.runCutoffs, cutoff)
	return 2, nil
}

func (s *recordingLifecycleStore) Compact(context.Context) error { return nil }

func TestDataLifecycleApplyRetentionDeletesRecordsOlderThanConfiguredDays(t *testing.T) {
	store := &recordingLifecycleStore{}
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	uc := NewDataLifecycleUseCase(store, RetentionPolicy{TraceDays: 30, EvaluationRunDays: 90})
	uc.now = func() time.Time { return now }

	got, err := uc.ApplyRetention(context.Background())

	if err != nil {
		t.Fatal(err)
	}
	if want := (RetentionReport{Traces: 3, EvaluationRuns: 2}); got != want {
		t.Fatalf("retention report = %+v, want %+v", got, want)
	}
	if want := []time.Time{now.AddDate(0, 0, -30)}; !reflect.DeepEqual(store.traceCutoffs, want) {
		t.Fatalf("trace cutoffs = %v, want %v", store.traceCutoffs, want)
	}
	if want := []time.Time{now.AddDate(0, 0, -90)}; !reflect.DeepEqual(store.runCutoffs, want) {
		t.Fatalf("evaluation run cutoffs = %v, want %v", store.runCutoffs, want)
	}
}

func TestDataLifecycleApplyRetentionWithZeroDaysKeepsEverything(t *testing.T) {
	store := &recordingLifecycleStore{}
	uc := NewDataLifecycleUseCase(store, RetentionPolicy{})

	got, err := uc.ApplyRetention(context.Background())

	if err != nil {
		t.Fatal(err)
	}
	if got != (RetentionReport{}) || len(store.traceCutoffs) != 0 || len(store.runCutoffs) != 0 {
		t.Fatalf("zero-day policy deleted data: report=%+v traceCutoffs=%v runCutoffs=%v", got, store.traceCutoffs, store.runCutoffs)
	}
}

type recordingAuditPurger struct{ cutoffs []time.Time }

func (p *recordingAuditPurger) DeleteEventsBefore(_ context.Context, cutoff time.Time) (int, error) {
	p.cutoffs = append(p.cutoffs, cutoff)
	return 4, nil
}

func TestDataLifecycleApplyRetentionPurgesAuditEventsOnlyWithTheirOwnPolicy(t *testing.T) {
	store := &recordingLifecycleStore{}
	purger := &recordingAuditPurger{}
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	uc := NewDataLifecycleUseCase(store, RetentionPolicy{AuditDays: 365})
	uc.AuditRetention = purger
	uc.now = func() time.Time { return now }

	got, err := uc.ApplyRetention(context.Background())

	if err != nil {
		t.Fatal(err)
	}
	if want := (RetentionReport{AuditEvents: 4}); got != want {
		t.Fatalf("retention report = %+v, want %+v", got, want)
	}
	if want := []time.Time{now.AddDate(0, 0, -365)}; !reflect.DeepEqual(purger.cutoffs, want) {
		t.Fatalf("audit cutoffs = %v, want %v", purger.cutoffs, want)
	}
	if len(store.traceCutoffs) != 0 || len(store.runCutoffs) != 0 {
		t.Fatal("audit retention must not delete traces or evaluation runs")
	}
}
