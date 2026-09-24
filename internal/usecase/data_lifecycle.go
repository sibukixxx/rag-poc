package usecase

import (
	"context"
	"fmt"
	"time"

	"github.com/sibukixxx/rag-poc/internal/domain/lifecycle"
)

// RetentionPolicy says how long operational artifacts are kept. Zero means
// "keep until explicitly deleted". Golden Dataset definitions, documents,
// and configuration are never removed by retention; only traces and
// evaluation runs (whose results can quote customer text) are.
type RetentionPolicy struct {
	TraceDays         int
	EvaluationRunDays int
}

// RetentionReport counts what one retention pass removed.
type RetentionReport struct {
	Traces         int `json:"traces"`
	EvaluationRuns int `json:"evaluation_runs"`
}

// DataLifecycleUseCase is the operator path for customer-data deletion and
// retention (#23). The HTTP API and the `forgeai data` CLI both call it.
type DataLifecycleUseCase struct {
	store  lifecycle.Store
	policy RetentionPolicy
	now    func() time.Time
}

func NewDataLifecycleUseCase(store lifecycle.Store, policy RetentionPolicy) *DataLifecycleUseCase {
	return &DataLifecycleUseCase{store: store, policy: policy, now: time.Now}
}

func (u *DataLifecycleUseCase) DeleteDocument(ctx context.Context, documentID string) (lifecycle.DeletionReport, error) {
	return u.store.DeleteDocument(ctx, documentID)
}

func (u *DataLifecycleUseCase) DeleteKnowledgeBase(ctx context.Context, knowledgeBaseID string, includeDeployments bool) (lifecycle.DeletionReport, error) {
	return u.store.DeleteKnowledgeBase(ctx, knowledgeBaseID, includeDeployments)
}

// ApplyRetention deletes traces and evaluation runs older than the policy.
func (u *DataLifecycleUseCase) ApplyRetention(ctx context.Context) (RetentionReport, error) {
	var report RetentionReport
	now := u.now().UTC()
	if u.policy.TraceDays > 0 {
		n, err := u.store.DeleteTracesStartedBefore(ctx, now.AddDate(0, 0, -u.policy.TraceDays))
		if err != nil {
			return report, fmt.Errorf("applying trace retention: %w", err)
		}
		report.Traces = n
	}
	if u.policy.EvaluationRunDays > 0 {
		n, err := u.store.DeleteEvaluationRunsStartedBefore(ctx, now.AddDate(0, 0, -u.policy.EvaluationRunDays))
		if err != nil {
			return report, fmt.Errorf("applying evaluation-run retention: %w", err)
		}
		report.EvaluationRuns = n
	}
	return report, nil
}

// Compact removes deleted content from the database files ForgeAI manages.
// It rebuilds the file, so operators run it explicitly after a purge.
func (u *DataLifecycleUseCase) Compact(ctx context.Context) error {
	return u.store.Compact(ctx)
}
