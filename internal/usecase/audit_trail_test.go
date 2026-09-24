package usecase_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/sibukixxx/rag-poc/internal/adapter/sqlite"
	"github.com/sibukixxx/rag-poc/internal/domain/audit"
	"github.com/sibukixxx/rag-poc/internal/domain/demoaccess"
	"github.com/sibukixxx/rag-poc/internal/usecase"
)

type auditFixture struct {
	db     *sql.DB
	audit  *sqlite.AuditStore
	kbID   string
	deploy *usecase.DeploymentUseCase
}

func newAuditFixture(t *testing.T) auditFixture {
	t.Helper()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "forgeai.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	ks := sqlite.NewKnowledgeStore(db)
	kb, err := ks.EnsureKnowledgeBase(ctx, "Support", "support")
	if err != nil {
		t.Fatal(err)
	}
	prompts := sqlite.NewPromptStore(db)
	p, err := prompts.EnsurePrompt(ctx, usecase.RAGPromptName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prompts.CreateVersion(ctx, p.ID, "PROMPT"); err != nil {
		t.Fatal(err)
	}
	auditStore := sqlite.NewAuditStore(db)
	deployUC := usecase.NewDeploymentUseCase(sqlite.NewDeploymentStore(db), ks, prompts, nil)
	deployUC.Audit = auditStore
	return auditFixture{db: db, audit: auditStore, kbID: kb.ID, deploy: deployUC}
}

// eventsByAction returns recorded events for action, oldest first.
func (f auditFixture) eventsByAction(t *testing.T, action string) []audit.Event {
	t.Helper()
	all, err := f.audit.ListEvents(context.Background(), 1000)
	if err != nil {
		t.Fatal(err)
	}
	var out []audit.Event
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].Action == action {
			out = append(out, all[i])
		}
	}
	return out
}

// assertAuditTableExcludes proves a secret appears nowhere in audit rows.
func (f auditFixture) assertAuditTableExcludes(t *testing.T, secret string) {
	t.Helper()
	var n int
	err := f.db.QueryRow(`SELECT COUNT(1) FROM audit_events
		WHERE instr(actor || target || metadata_json || action, ?) > 0`, secret).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d audit rows contain the secret in plaintext", n)
	}
}

func TestDeploymentAuditRecordsCreateIssueAndRevokeWithoutTokenPlaintext(t *testing.T) {
	f := newAuditFixture(t)
	ctx := audit.WithActor(context.Background(), "http:203.0.113.4")

	d, err := f.deploy.Create(ctx, usecase.CreateDeploymentInput{Slug: "support-prod", KnowledgeBaseID: f.kbID})
	if err != nil {
		t.Fatal(err)
	}
	issued, err := f.deploy.IssueToken(ctx, d.ID, "ci")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.deploy.RevokeToken(ctx, d.ID, issued.Token.ID); err != nil {
		t.Fatal(err)
	}

	create := f.eventsByAction(t, audit.ActionDeploymentCreate)
	if len(create) != 1 || create[0].Target != "deployment:"+d.ID || create[0].Actor != "http:203.0.113.4" ||
		create[0].Metadata["knowledge_base_id"] != f.kbID || create[0].Metadata["prompt_version"] != "1" {
		t.Fatalf("deployment.create events = %+v", create)
	}
	issue := f.eventsByAction(t, audit.ActionRuntimeTokenIssue)
	wantMeta := map[string]string{"token_id": issued.Token.ID, "token_fingerprint": audit.Fingerprint(issued.Secret)}
	if len(issue) != 1 || issue[0].Outcome != audit.OutcomeSuccess || issue[0].Metadata["token_id"] != wantMeta["token_id"] ||
		issue[0].Metadata["token_fingerprint"] != wantMeta["token_fingerprint"] {
		t.Fatalf("runtime_token.issue events = %+v, want metadata %v", issue, wantMeta)
	}
	if revoke := f.eventsByAction(t, audit.ActionRuntimeTokenRevoke); len(revoke) != 1 || revoke[0].Metadata["token_id"] != issued.Token.ID {
		t.Fatalf("runtime_token.revoke events = %+v", revoke)
	}
	f.assertAuditTableExcludes(t, issued.Secret)
}

func TestRuntimeAuthenticateRecordsRejectionWithFingerprintOnly(t *testing.T) {
	f := newAuditFixture(t)
	ctx := context.Background()
	d, err := f.deploy.Create(ctx, usecase.CreateDeploymentInput{Slug: "support-prod", KnowledgeBaseID: f.kbID})
	if err != nil {
		t.Fatal(err)
	}
	issued, err := f.deploy.IssueToken(ctx, d.ID, "ci")
	if err != nil {
		t.Fatal(err)
	}
	runtimeUC := usecase.NewRuntimeUseCase(sqlite.NewDeploymentStore(f.db), nil, nil)
	runtimeUC.Audit = f.audit
	const wrongToken = "fai_this_is_not_a_valid_token"

	if _, _, err := runtimeUC.Authenticate(ctx, "support-prod", issued.Secret); err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}
	_, _, err = runtimeUC.Authenticate(ctx, "support-prod", wrongToken)

	if !errors.Is(err, usecase.ErrUnauthorizedRuntime) {
		t.Fatalf("wrong token error = %v", err)
	}
	rejected := f.eventsByAction(t, audit.ActionRuntimeAuthRejected)
	if len(rejected) != 1 || rejected[0].Outcome != audit.OutcomeDenied || rejected[0].Target != "deployment_slug:support-prod" ||
		rejected[0].Metadata["token_fingerprint"] != audit.Fingerprint(wrongToken) {
		t.Fatalf("runtime_token.rejected events = %+v (successful auth must not be recorded)", rejected)
	}
	f.assertAuditTableExcludes(t, wrongToken)
	f.assertAuditTableExcludes(t, issued.Secret)
}

func TestDataLifecycleAuditRecordsDeletionsAndRefusals(t *testing.T) {
	f := newAuditFixture(t)
	ctx := audit.WithActor(context.Background(), "cli")
	if _, err := f.deploy.Create(ctx, usecase.CreateDeploymentInput{Slug: "support-prod", KnowledgeBaseID: f.kbID}); err != nil {
		t.Fatal(err)
	}
	uc := usecase.NewDataLifecycleUseCase(sqlite.NewLifecycleStore(f.db), usecase.RetentionPolicy{})
	uc.Audit = f.audit

	_, refusedErr := uc.DeleteKnowledgeBase(ctx, f.kbID, false)
	if _, err := uc.DeleteKnowledgeBase(ctx, f.kbID, true); err != nil {
		t.Fatal(err)
	}

	if refusedErr == nil {
		t.Fatal("expected the first deletion to be refused")
	}
	got := f.eventsByAction(t, audit.ActionKnowledgeBaseDelete)
	if len(got) != 2 {
		t.Fatalf("knowledge_base.delete events = %+v, want refusal then success", got)
	}
	if got[0].Outcome != audit.OutcomeDenied || got[1].Outcome != audit.OutcomeSuccess ||
		got[1].Target != "knowledge_base:"+f.kbID || got[1].Actor != "cli" || got[1].Metadata["deployments"] != "1" {
		t.Fatalf("knowledge_base.delete events = %+v", got)
	}
}

func TestDemoLoginAuditRecordsSuccessAndFailureWithoutPassword(t *testing.T) {
	f := newAuditFixture(t)
	ctx := context.Background()
	uc := usecase.NewDemoAccessUseCase(sqlite.NewDemoAccessStore(f.db), time.Hour)
	uc.Audit = f.audit
	_, password, err := uc.CreateUser(ctx, "buyer", "buyer@example.com", "Example", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	if _, _, _, err := uc.Authenticate(ctx, "buyer", password, ""); err != nil {
		t.Fatal(err)
	}
	_, _, _, err = uc.Authenticate(ctx, "buyer", "wrong-password-value", "")

	if !errors.Is(err, demoaccess.ErrInvalidCredentials) {
		t.Fatalf("wrong password error = %v", err)
	}
	got := f.eventsByAction(t, audit.ActionAuthLogin)
	if len(got) != 2 || got[0].Outcome != audit.OutcomeSuccess || got[1].Outcome != audit.OutcomeFailure ||
		got[0].Target != "demo_user:buyer" || got[1].Target != "demo_user:buyer" {
		t.Fatalf("auth.login events = %+v", got)
	}
	f.assertAuditTableExcludes(t, password)
	f.assertAuditTableExcludes(t, "wrong-password-value")
	if got[1].Metadata["reason"] != "invalid_credentials" {
		t.Fatalf("failure reason = %q, want the generic invalid_credentials", got[1].Metadata["reason"])
	}
}
