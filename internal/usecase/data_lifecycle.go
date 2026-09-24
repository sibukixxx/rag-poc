package usecase

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/sibukixxx/rag-poc/internal/domain/audit"
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
	Audit  audit.Recorder
}

func NewDataLifecycleUseCase(store lifecycle.Store, policy RetentionPolicy) *DataLifecycleUseCase {
	return &DataLifecycleUseCase{store: store, policy: policy, now: time.Now}
}

func (u *DataLifecycleUseCase) DeleteDocument(ctx context.Context, documentID string) (lifecycle.DeletionReport, error) {
	report, err := u.store.DeleteDocument(ctx, documentID)
	u.auditDeletion(ctx, audit.ActionDocumentDelete, "document:"+documentID, report, err)
	return report, err
}

func (u *DataLifecycleUseCase) DeleteKnowledgeBase(ctx context.Context, knowledgeBaseID string, includeDeployments bool) (lifecycle.DeletionReport, error) {
	report, err := u.store.DeleteKnowledgeBase(ctx, knowledgeBaseID, includeDeployments)
	u.auditDeletion(ctx, audit.ActionKnowledgeBaseDelete, "knowledge_base:"+knowledgeBaseID, report, err)
	return report, err
}

func (u *DataLifecycleUseCase) auditDeletion(ctx context.Context, action, target string, r lifecycle.DeletionReport, err error) {
	switch {
	case errors.Is(err, lifecycle.ErrKnowledgeBaseHasDeployments):
		recordAudit(ctx, u.Audit, action, audit.OutcomeDenied, target, map[string]string{"reason": "has_deployments"})
	case err != nil:
		recordAudit(ctx, u.Audit, action, audit.OutcomeFailure, target, nil)
	default:
		recordAudit(ctx, u.Audit, action, audit.OutcomeSuccess, target, map[string]string{
			"documents": strconv.Itoa(r.Documents), "chunks": strconv.Itoa(r.Chunks),
			"datasets": strconv.Itoa(r.Datasets), "deployments": strconv.Itoa(r.Deployments),
			"source_connections": strconv.Itoa(r.SourceConnections),
		})
	}
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
