package usecase

import (
	"context"
	"time"

	"github.com/sibukixxx/rag-poc/internal/domain/audit"
)

// recordAudit writes one audit event. A nil recorder (tests, callers that
// have not wired auditing) records nothing, and a failed write never changes
// the outcome of the audited operation: the store logs the failure itself.
func recordAudit(ctx context.Context, rec audit.Recorder, action string, outcome audit.Outcome, target string, meta map[string]string) {
	if rec == nil {
		return
	}
	_ = rec.Record(ctx, audit.Event{
		OccurredAt: time.Now(), Action: action, Outcome: outcome,
		Actor: audit.ActorFrom(ctx), Target: target, Metadata: meta,
	})
}
