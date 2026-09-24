// Package audit records security-relevant actions (#24): who did what,
// to which object, when, and whether it succeeded. Events carry IDs,
// fingerprints, and operation metadata only — never secrets, prompts, or
// document text. docs/security/AUDIT_TRAIL.md lists the recorded actions.
package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// Action names. Keep them stable: operators filter and alert on them.
const (
	ActionAuthLogin           = "auth.login"
	ActionRuntimeTokenIssue   = "runtime_token.issue"
	ActionRuntimeTokenRevoke  = "runtime_token.revoke"
	ActionRuntimeAuthRejected = "runtime_token.rejected"
	ActionDeploymentCreate    = "deployment.create"
	ActionDocumentDelete      = "document.delete"
	ActionKnowledgeBaseDelete = "knowledge_base.delete"
	ActionProviderInvoke      = "provider.invoke"
	ActionServerStart         = "server.start"
	ActionSecretSet           = "secret.set"
	ActionSecretDelete        = "secret.delete"
)

type Outcome string

const (
	OutcomeSuccess Outcome = "success"
	OutcomeFailure Outcome = "failure"
	OutcomeDenied  Outcome = "denied"
)

// Event is one audit record. Metadata values must be safe to show to an
// operator: identifiers, counts, model names, endpoint hosts, rule names.
type Event struct {
	ID         string            `json:"id"`
	OccurredAt time.Time         `json:"occurred_at"`
	Action     string            `json:"action"`
	Outcome    Outcome           `json:"outcome"`
	Actor      string            `json:"actor"`
	Target     string            `json:"target,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

// Recorder persists events. Recording is best-effort from the caller's
// point of view: a failed audit write must not change the result of the
// audited operation, so callers use Record and ignore its error only after
// it has been logged by the implementation.
type Recorder interface {
	Record(ctx context.Context, e Event) error
}

// Store adds operator read access and retention on top of Recorder.
type Store interface {
	Recorder
	ListEvents(ctx context.Context, limit int) ([]Event, error)
	DeleteEventsBefore(ctx context.Context, cutoff time.Time) (int, error)
}

// Nop discards events; it is the default when no audit store is wired.
type Nop struct{}

func (Nop) Record(context.Context, Event) error { return nil }

type actorKey struct{}

// WithActor attaches the acting principal (e.g. "demo_user:alice",
// "runtime_token:3f2a…", "cli", "http:203.0.113.4") to ctx.
func WithActor(ctx context.Context, actor string) context.Context {
	return context.WithValue(ctx, actorKey{}, actor)
}

// ActorFrom returns the actor attached to ctx, or "system" when none is.
func ActorFrom(ctx context.Context) string {
	if a, ok := ctx.Value(actorKey{}).(string); ok && a != "" {
		return a
	}
	return "system"
}

// Fingerprint returns a short, non-reversible identifier for a secret so
// events can be correlated without storing the secret. For runtime tokens
// it is a prefix of the same SHA-256 hash ForgeAI stores for the token.
func Fingerprint(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])[:12]
}
