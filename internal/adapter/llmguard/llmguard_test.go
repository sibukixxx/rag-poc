package llmguard_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/sibukixxx/rag-poc/internal/adapter/llmguard"
	"github.com/sibukixxx/rag-poc/internal/domain/audit"
	"github.com/sibukixxx/rag-poc/internal/domain/llm"
	"github.com/sibukixxx/rag-poc/internal/domain/outbound"
)

type recordingLLM struct{ requests []llm.GenerateRequest }

func (r *recordingLLM) Generate(_ context.Context, req llm.GenerateRequest) (*llm.GenerateResponse, error) {
	r.requests = append(r.requests, req)
	return &llm.GenerateResponse{Content: "ok"}, nil
}

func (r *recordingLLM) Stream(_ context.Context, req llm.GenerateRequest) (<-chan llm.StreamEvent, error) {
	r.requests = append(r.requests, req)
	ch := make(chan llm.StreamEvent)
	close(ch)
	return ch, nil
}

type recordingEmbedder struct{ inputs [][]string }

func (r *recordingEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	r.inputs = append(r.inputs, texts)
	return make([][]float32, len(texts)), nil
}
func (r *recordingEmbedder) Dimensions() int { return 2 }
func (r *recordingEmbedder) Model() string   { return "embed" }

type memoryRecorder struct{ events []audit.Event }

func (m *memoryRecorder) Record(_ context.Context, e audit.Event) error {
	m.events = append(m.events, e)
	return nil
}

const email = "hanako@example.com"

func newGuard(t *testing.T, policy outbound.Policy, rec audit.Recorder) *llmguard.Guard {
	t.Helper()
	d, err := outbound.NewDetector([]string{"email", "phone"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return llmguard.New(policy, d, rec)
}

func sensitiveRequest() llm.GenerateRequest {
	return llm.GenerateRequest{Model: "m", Messages: []llm.Message{
		{Role: llm.RoleSystem, Content: "answer from context"},
		{Role: llm.RoleUser, Content: "Context: contact " + email + " or 090-1234-5678"},
	}}
}

func TestDenySensitiveBlocksBeforeTheProviderIsCalled(t *testing.T) {
	next := &recordingLLM{}
	rec := &memoryRecorder{}
	guarded := newGuard(t, outbound.PolicyDenySensitive, rec).WrapLLM(next, "openai")

	_, genErr := guarded.Generate(context.Background(), sensitiveRequest())
	_, streamErr := guarded.Stream(context.Background(), sensitiveRequest())

	if !errors.Is(genErr, outbound.ErrBlocked) || !errors.Is(streamErr, outbound.ErrBlocked) {
		t.Fatalf("errors = %v / %v, want ErrBlocked", genErr, streamErr)
	}
	if len(next.requests) != 0 {
		t.Fatalf("provider received %d requests under deny_sensitive, want 0", len(next.requests))
	}
	want := map[string]string{"policy": "deny_sensitive", "matches": "email=1,phone=1", "operation": "generate", "provider": "openai", "model": "m"}
	if len(rec.events) != 2 || rec.events[0].Action != audit.ActionEgressPolicyApplied || rec.events[0].Outcome != audit.OutcomeDenied ||
		!reflect.DeepEqual(rec.events[0].Metadata, want) {
		t.Fatalf("audit events = %+v, want denied egress_policy.apply with %v", rec.events, want)
	}
}

func TestRedactSendsRedactedCopyAndLeavesCallerRequestUntouched(t *testing.T) {
	next := &recordingLLM{}
	rec := &memoryRecorder{}
	guarded := newGuard(t, outbound.PolicyRedactKnownPatterns, rec).WrapLLM(next, "openai")
	req := sensitiveRequest()

	if _, err := guarded.Generate(context.Background(), req); err != nil {
		t.Fatal(err)
	}

	if got := next.requests[0].Messages[1].Content; got != "Context: contact [REDACTED:email] or [REDACTED:phone]" {
		t.Fatalf("provider received %q", got)
	}
	if req.Messages[1].Content != sensitiveRequest().Messages[1].Content {
		t.Fatal("caller's request was mutated")
	}
	raw, _ := json.Marshal(rec.events)
	if strings.Contains(string(raw), email) || strings.Contains(string(raw), "090-1234-5678") {
		t.Fatalf("audit event contains the original value: %s", raw)
	}
	if len(rec.events) != 1 || rec.events[0].Outcome != audit.OutcomeSuccess || rec.events[0].Metadata["policy"] != "redact_known_patterns" {
		t.Fatalf("audit events = %+v", rec.events)
	}
}

func TestCleanRequestsPassThroughWithoutAuditNoise(t *testing.T) {
	next := &recordingLLM{}
	rec := &memoryRecorder{}
	guarded := newGuard(t, outbound.PolicyDenySensitive, rec).WrapLLM(next, "openai")
	req := llm.GenerateRequest{Model: "m", Messages: []llm.Message{{Role: llm.RoleUser, Content: "What is the refund window?"}}}

	if _, err := guarded.Generate(context.Background(), req); err != nil {
		t.Fatal(err)
	}

	if len(next.requests) != 1 || !reflect.DeepEqual(next.requests[0], req) || len(rec.events) != 0 {
		t.Fatalf("clean request altered or audited: requests=%+v events=%+v", next.requests, rec.events)
	}
}

func TestEmbedderIsGuardedToo(t *testing.T) {
	deny := &recordingEmbedder{}
	_, err := newGuard(t, outbound.PolicyDenySensitive, nil).WrapEmbedder(deny, "embedding").Embed(context.Background(), []string{"ok", "mail " + email})
	if !errors.Is(err, outbound.ErrBlocked) || len(deny.inputs) != 0 {
		t.Fatalf("deny: err=%v inputs=%v", err, deny.inputs)
	}

	redact := &recordingEmbedder{}
	if _, err := newGuard(t, outbound.PolicyRedactKnownPatterns, nil).WrapEmbedder(redact, "embedding").Embed(context.Background(), []string{"ok", "mail " + email}); err != nil {
		t.Fatal(err)
	}
	if want := [][]string{{"ok", "mail [REDACTED:email]"}}; !reflect.DeepEqual(redact.inputs, want) {
		t.Fatalf("embedder inputs = %v, want %v", redact.inputs, want)
	}
}

func TestAllowPolicyReturnsTheProviderUnwrapped(t *testing.T) {
	next := &recordingLLM{}

	got := newGuard(t, outbound.PolicyAllow, nil).WrapLLM(next, "openai")

	if got != llm.LLM(next) {
		t.Fatal("allow policy should not wrap the provider")
	}
}
