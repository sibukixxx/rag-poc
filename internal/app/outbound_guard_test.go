package app

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

const (
	guardEmail = "hanako.sato@customer.example"
	guardPhone = "090-1234-5678"
	guardEmpID = "EMP-778899"
)

const guardRules = `  outbound_policy: %s
  sensitive_rules:
    - name: employee_id
      pattern: 'EMP-\d{6}'
`

// TestOutboundRedactionRemovesValuesFromEveryProviderPayload drives ingest
// (embeddings), search with LLM rerank, RAG chat, and judged evaluation
// through the real wiring and inspects the bytes the provider received.
func TestOutboundRedactionRemovesValuesFromEveryProviderPayload(t *testing.T) {
	provider := newFakeProvider(t, 64)
	configPath := writeE2EConfig(t, provider.baseURL(), fmt.Sprintf(guardRules, "redact_known_patterns"))
	t.Setenv("FORGEAI_E2E_PROVIDER_KEY", "fake-key")
	t.Setenv("FORGEAI_DEMO_AUTH_ENABLED", "")
	srv := startE2EServer(t, configPath)

	var kb struct {
		ID string `json:"id"`
	}
	srv.postJSON(t, "/api/v1/knowledge-bases", `{"name":"Guard","slug":"guard"}`, http.StatusOK, &kb)
	srv.upload(t, "/api/v1/knowledge-bases/"+kb.ID+"/documents", "contacts.md",
		"# Escalation\n\nEscalate refunds to "+guardEmail+" or call "+guardPhone+". Owner "+guardEmpID+".")
	srv.postJSON(t, "/api/v1/knowledge-bases/"+kb.ID+"/search", `{"query":"who handles refund escalation","top_k":3,"rerank":true}`, http.StatusOK, nil)
	srv.postSSE(t, "/api/v1/knowledge-bases/"+kb.ID+"/chat", `{"alias":"normal","query":"Who handles refund escalation? Reply to `+guardEmail+`"}`, "")
	var dataset struct {
		ID string `json:"id"`
	}
	srv.postJSON(t, "/api/v1/datasets", fmt.Sprintf(`{"name":"guard","knowledge_base_id":%q}`, kb.ID), http.StatusOK, &dataset)
	srv.postJSON(t, "/api/v1/datasets/"+dataset.ID+"/cases", `[{"query":"refund escalation contact","expected_filenames":["contacts.md"],"expected_answer":"`+guardEmail+`"}]`, http.StatusOK, nil)
	srv.runEvaluation(t, dataset.ID, 3, true)

	bodies := provider.requestBodies()
	var sawRedaction, sawRerank, sawJudge bool
	for _, b := range bodies {
		for _, secret := range []string{guardEmail, guardPhone, guardEmpID} {
			if strings.Contains(b, secret) {
				t.Fatalf("provider received %q in payload: %s", secret, b)
			}
		}
		sawRedaction = sawRedaction || strings.Contains(b, "[REDACTED:email]")
		sawRerank = sawRerank || strings.Contains(strings.ToLower(b), "rank")
		sawJudge = sawJudge || strings.Contains(b, "groundedness")
	}
	if !sawRedaction || !sawRerank || !sawJudge {
		t.Fatalf("paths not exercised: redaction=%v rerank=%v judge=%v (%d payloads)", sawRedaction, sawRerank, sawJudge, len(bodies))
	}
}

func TestOutboundDenyBlocksProviderRequestsAndReturnsStableError(t *testing.T) {
	provider := newFakeProvider(t, 64)
	configPath := writeE2EConfig(t, provider.baseURL(), fmt.Sprintf(guardRules, "deny_sensitive"))
	t.Setenv("FORGEAI_E2E_PROVIDER_KEY", "fake-key")
	t.Setenv("FORGEAI_DEMO_AUTH_ENABLED", "")
	srv := startE2EServer(t, configPath)
	var kb struct {
		ID string `json:"id"`
	}
	srv.postJSON(t, "/api/v1/knowledge-bases", `{"name":"Guard","slug":"guard"}`, http.StatusOK, &kb)
	srv.upload(t, "/api/v1/knowledge-bases/"+kb.ID+"/documents", "policy.md", "# Refunds\n\nRefunds are accepted within 30 days.")
	before := provider.requestCount()

	body := srv.do(t, http.MethodPost, "/api/v1/knowledge-bases/"+kb.ID+"/chat",
		`{"alias":"normal","query":"Send the refund policy to `+guardEmail+`"}`, "", http.StatusUnprocessableEntity)

	if got := strings.TrimSpace(string(body)); got != "request blocked by outbound sensitive-data policy" {
		t.Fatalf("blocked response body = %q", got)
	}
	if after := provider.requestCount(); after != before {
		t.Fatalf("provider received %d requests after the block, want 0", after-before)
	}
	auditBody := string(srv.do(t, http.MethodGet, "/api/v1/audit-events?limit=100", "", "", http.StatusOK))
	if !strings.Contains(auditBody, `"egress_policy.apply"`) || strings.Contains(auditBody, guardEmail) {
		t.Fatalf("audit trail must record the block without the value: %s", auditBody)
	}
}
