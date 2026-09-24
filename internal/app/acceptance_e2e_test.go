package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAcceptanceE2EMockProviderJourney is the G1/G2 deterministic gate from
// docs/V0.1_ACCEPTANCE.md: a clean database driven through the real HTTP
// surface — ingest, hybrid search, cited RAG chat, Golden Dataset with
// judge, Before/After compare, Deployment, Runtime search/chat, prompt
// snapshot immutability, restart persistence, token revocation, and
// customer-data deletion, and the security audit trail.
// Only the model provider is replaced (by a loopback fake), and Private
// Mode is on, so the run also proves no other destination is needed.
func TestAcceptanceE2EMockProviderJourney(t *testing.T) {
	if testing.Short() {
		t.Skip("acceptance E2E skipped in -short mode")
	}
	provider := newFakeProvider(t, 64)
	configPath := writeE2EConfig(t, provider.baseURL())
	t.Setenv("FORGEAI_E2E_PROVIDER_KEY", "fake-key")
	t.Setenv("FORGEAI_DEMO_AUTH_ENABLED", "")
	t.Setenv("FORGEAI_PRIVACY_MODE", "")

	srv := startE2EServer(t, configPath)

	// 1. Ingest into a fresh knowledge base.
	var kb struct {
		ID string `json:"id"`
	}
	srv.postJSON(t, "/api/v1/knowledge-bases", `{"name":"Acceptance","slug":"acceptance"}`, http.StatusOK, &kb)
	docs := map[string]string{
		"refund.md":   "# Refund policy\n\nCustomers may request a refund within 30 days of purchase. Refunds are paid to the original card.",
		"shipping.md": "# Shipping\n\nOrders ship from the Osaka warehouse within two business days. Express shipping costs extra.",
		"security.md": "# Security\n\nAPI keys are encrypted with AES-GCM. Rotate the master key through the documented procedure.",
	}
	for name, body := range docs {
		srv.upload(t, "/api/v1/knowledge-bases/"+kb.ID+"/documents", name, body)
	}

	// 2. Hybrid search ranks the lexically matching document first.
	var search struct {
		Results []struct {
			Filename string `json:"filename"`
		} `json:"results"`
	}
	srv.postJSON(t, "/api/v1/knowledge-bases/"+kb.ID+"/search", `{"query":"refund within 30 days","top_k":3}`, http.StatusOK, &search)
	if len(search.Results) == 0 || search.Results[0].Filename != "refund.md" {
		t.Fatalf("top search result = %+v, want refund.md first", search.Results)
	}

	// 3. Cited RAG chat streams an answer and a terminal event with citations.
	chat := srv.postSSE(t, "/api/v1/knowledge-bases/"+kb.ID+"/chat", `{"alias":"normal","query":"How long do customers have to request a refund?"}`, "")
	assertCitedDoneEvent(t, "management chat", chat, "refund.md")

	// 4. Golden Dataset + retrieval metrics + judge, twice, then compare.
	var dataset struct {
		ID string `json:"id"`
	}
	srv.postJSON(t, "/api/v1/datasets", fmt.Sprintf(`{"name":"acceptance","knowledge_base_id":%q}`, kb.ID), http.StatusOK, &dataset)
	cases := `[
	  {"query":"refund within 30 days","expected_filenames":["refund.md"],"expected_answer":"30 days"},
	  {"query":"Osaka warehouse shipping","expected_filenames":["shipping.md"]},
	  {"query":"AES-GCM master key rotation","expected_filenames":["security.md"]}
	]`
	srv.postJSON(t, "/api/v1/datasets/"+dataset.ID+"/cases", cases, http.StatusOK, nil)
	runA := srv.runEvaluation(t, dataset.ID, 3, true)
	runB := srv.runEvaluation(t, dataset.ID, 1, true)
	if runA.HitRate != 1 || runA.Groundedness != 1 {
		t.Fatalf("run A = %+v, want hit_rate=1 and groundedness=1 from the fake judge", runA)
	}
	var cmp struct {
		Winner   string `json:"winner"`
		Judged   bool   `json:"judged"`
		Markdown string `json:"markdown"`
	}
	srv.getJSON(t, "/api/v1/evaluations/compare?a="+runA.ID+"&b="+runB.ID, http.StatusOK, &cmp)
	if !cmp.Judged || cmp.Winner == "" || !strings.Contains(cmp.Markdown, "|") {
		t.Fatalf("compare = winner %q judged %v markdown %d bytes; want a judged comparison with a Markdown table", cmp.Winner, cmp.Judged, len(cmp.Markdown))
	}

	// 5. Deployment + runtime token.
	var dep struct {
		ID            string `json:"id"`
		PromptVersion int    `json:"prompt_version"`
	}
	srv.postJSON(t, "/api/v1/deployments", fmt.Sprintf(`{"slug":"acceptance-app","knowledge_base_id":%q,"alias":"normal","top_k":3}`, kb.ID), http.StatusCreated, &dep)
	var tok struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	srv.postJSON(t, "/api/v1/deployments/"+dep.ID+"/tokens", `{"name":"acceptance"}`, http.StatusCreated, &tok)
	if tok.Token == "" {
		t.Fatal("token issuance did not return the one-time plaintext token")
	}

	srv.runtimeSearch(t, "acceptance-app", tok.Token, http.StatusOK)
	runtimeChat := srv.postSSE(t, "/runtime/v1/apps/acceptance-app/chat", `{"query":"refund window?"}`, tok.Token)
	assertCitedDoneEvent(t, "runtime chat", runtimeChat, "refund.md")
	frozenPrompt := provider.lastSystemPrompt()

	// 6. Activating a new management prompt must not change the Deployment.
	promptID := srv.ragPromptID(t)
	srv.postJSON(t, "/api/v1/prompts/"+promptID+"/versions", `{"content":"CHANGED ACTIVE PROMPT"}`, http.StatusOK, nil)
	srv.postJSON(t, "/api/v1/prompts/"+promptID+"/activate", fmt.Sprintf(`{"version":%d}`, dep.PromptVersion+1), http.StatusOK, nil)
	srv.postSSE(t, "/runtime/v1/apps/acceptance-app/chat", `{"query":"refund window?"}`, tok.Token)
	if got := provider.lastSystemPrompt(); got != frozenPrompt {
		t.Fatalf("runtime system prompt changed after activation: got %q, want frozen %q", got, frozenPrompt)
	}

	// 7. Restart: a new process on the same database keeps the deployment and token.
	srv.close()
	srv = startE2EServer(t, configPath)
	srv.runtimeSearch(t, "acceptance-app", tok.Token, http.StatusOK)

	// 8. Revocation stops runtime search and chat.
	srv.do(t, http.MethodDelete, "/api/v1/deployments/"+dep.ID+"/tokens/"+tok.ID, "", "", http.StatusNoContent)
	srv.runtimeSearch(t, "acceptance-app", tok.Token, http.StatusUnauthorized)
	srv.do(t, http.MethodPost, "/runtime/v1/apps/acceptance-app/chat", `{"query":"refund window?"}`, tok.Token, http.StatusUnauthorized)

	// 9. Customer-data deletion (#23): a KB serving a Deployment is only
	// deleted with explicit opt-in, after which its content is unsearchable.
	srv.do(t, http.MethodDelete, "/api/v1/knowledge-bases/"+kb.ID, "", "", http.StatusConflict)
	srv.do(t, http.MethodDelete, "/api/v1/knowledge-bases/"+kb.ID+"?include_deployments=true", "", "", http.StatusOK)
	var remaining []struct {
		ID string `json:"id"`
	}
	srv.getJSON(t, "/api/v1/knowledge-bases", http.StatusOK, &remaining)
	if len(remaining) != 0 {
		t.Fatalf("knowledge bases after deletion = %+v, want none", remaining)
	}

	// 10. Security audit trail (#24): critical actions are recorded with
	// actors and IDs, and the one-time token plaintext is nowhere in it.
	auditBody := srv.do(t, http.MethodGet, "/api/v1/audit-events?limit=1000", "", "", http.StatusOK)
	if strings.Contains(string(auditBody), tok.Token) {
		t.Fatal("audit trail contains the runtime token plaintext")
	}
	var events []struct {
		Action  string `json:"action"`
		Outcome string `json:"outcome"`
		Actor   string `json:"actor"`
	}
	decode(t, auditBody, &events)
	seen := map[string]string{}
	runtimeAttributed := false
	for _, e := range events {
		seen[e.Action+"/"+e.Outcome] = e.Actor
		if e.Action == "provider.invoke" && strings.HasPrefix(e.Actor, "runtime_token:") {
			runtimeAttributed = true
		}
	}
	if !runtimeAttributed {
		t.Error("no provider.invoke event is attributed to the runtime token that caused it")
	}
	for _, want := range []string{
		"deployment.create/success", "runtime_token.issue/success", "runtime_token.revoke/success",
		"runtime_token.rejected/denied", "knowledge_base.delete/denied", "knowledge_base.delete/success",
		"provider.invoke/success",
	} {
		if _, ok := seen[want]; !ok {
			t.Errorf("audit trail has no %s event; recorded: %v", want, seen)
		}
	}
	if actor := seen["runtime_token.issue/success"]; actor != "http:127.0.0.1" {
		t.Errorf("token issuance actor = %q, want the management client address", actor)
	}

	if provider.requestCount() == 0 {
		t.Fatal("fake provider was never called; the journey did not exercise the provider path")
	}
}

func writeE2EConfig(t *testing.T, providerURL string) string {
	t.Helper()
	dir := t.TempDir()
	cfg := fmt.Sprintf(`server:
  port: 0
database:
  type: sqlite
  path: %[1]s/data/forgeai.db
storage:
  type: filesystem
  path: %[1]s/data/files
security:
  encryption_key_env: FORGEAI_E2E_MASTER_KEY
privacy:
  mode: local_only
llm:
  providers:
    fake:
      type: openai_compatible
      base_url: %[2]s
      api_key_env: FORGEAI_E2E_PROVIDER_KEY
  aliases:
    cheap:  {provider: fake, model: fake-model}
    normal: {provider: fake, model: fake-model}
    judge:  {provider: fake, model: fake-model}
embedding:
  provider:
    type: openai_compatible
    base_url: %[2]s
    api_key_env: FORGEAI_E2E_PROVIDER_KEY
  model: fake-embed
  dimensions: 64
`, dir, providerURL)
	path := filepath.Join(dir, "forgeai.yaml")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

type e2eServer struct {
	app  *App
	http *httptest.Server
}

func startE2EServer(t *testing.T, configPath string) *e2eServer {
	t.Helper()
	a, err := Bootstrap(configPath)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	h, err := a.Handler()
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	s := &e2eServer{app: a, http: httptest.NewServer(h)}
	t.Cleanup(s.close)
	return s
}

func (s *e2eServer) close() {
	if s.http != nil {
		s.http.Close()
		s.http = nil
	}
	if s.app != nil {
		_ = s.app.Close()
		s.app = nil
	}
}

func (s *e2eServer) do(t *testing.T, method, path, body, bearer string, want int) []byte {
	t.Helper()
	req, err := http.NewRequest(method, s.http.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	return s.send(t, req, want)
}

func (s *e2eServer) send(t *testing.T, req *http.Request, want int) []byte {
	t.Helper()
	resp, err := s.http.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", req.Method, req.URL.Path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		t.Fatalf("%s %s: status %d, want %d; body=%s", req.Method, req.URL.Path, resp.StatusCode, want, data)
	}
	return data
}

func (s *e2eServer) postJSON(t *testing.T, path, body string, want int, out any) {
	t.Helper()
	decode(t, s.do(t, http.MethodPost, path, body, "", want), out)
}

func (s *e2eServer) getJSON(t *testing.T, path string, want int, out any) {
	t.Helper()
	decode(t, s.do(t, http.MethodGet, path, "", "", want), out)
}

func decode(t *testing.T, data []byte, out any) {
	t.Helper()
	if out == nil {
		return
	}
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatalf("decoding %s: %v", data, err)
	}
}

func (s *e2eServer) upload(t *testing.T, path, filename, content string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write([]byte(content))
	_ = mw.Close()
	req, err := http.NewRequest(http.MethodPost, s.http.URL+path, &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	var doc struct {
		Status     string `json:"status"`
		ChunkCount int    `json:"chunk_count"`
	}
	decode(t, s.send(t, req, http.StatusOK), &doc)
	if doc.Status != "ready" || doc.ChunkCount == 0 {
		t.Fatalf("upload %s = %+v, want ready with chunks", filename, doc)
	}
}

type sseEvent struct {
	Delta     string `json:"delta"`
	Done      bool   `json:"done"`
	Error     string `json:"error"`
	Citations []struct {
		Filename string `json:"filename"`
	} `json:"citations"`
}

func (s *e2eServer) postSSE(t *testing.T, path, body, bearer string) []sseEvent {
	t.Helper()
	data := s.do(t, http.MethodPost, path, body, bearer, http.StatusOK)
	var events []sseEvent
	for _, line := range strings.Split(string(data), "\n") {
		payload, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}
		var ev sseEvent
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			t.Fatalf("decoding SSE line %q: %v", line, err)
		}
		events = append(events, ev)
	}
	return events
}

func assertCitedDoneEvent(t *testing.T, label string, events []sseEvent, wantFile string) {
	t.Helper()
	var answer strings.Builder
	for _, ev := range events {
		if ev.Error != "" {
			t.Fatalf("%s: stream error %q", label, ev.Error)
		}
		answer.WriteString(ev.Delta)
		if !ev.Done {
			continue
		}
		for _, c := range ev.Citations {
			if c.Filename == wantFile {
				if answer.String() != "The answer is in the cited passage [1]." {
					t.Fatalf("%s: streamed answer = %q", label, answer.String())
				}
				return
			}
		}
		t.Fatalf("%s: done event citations %+v do not include %s", label, ev.Citations, wantFile)
	}
	t.Fatalf("%s: no done event in %+v", label, events)
}

type e2eRun struct {
	ID           string  `json:"id"`
	Status       string  `json:"status"`
	Error        string  `json:"error"`
	HitRate      float64 `json:"hit_rate"`
	Groundedness float64 `json:"groundedness"`
}

func (s *e2eServer) runEvaluation(t *testing.T, datasetID string, topK int, judge bool) e2eRun {
	t.Helper()
	var run e2eRun
	s.postJSON(t, "/api/v1/evaluations", fmt.Sprintf(`{"dataset_id":%q,"top_k":%d,"judge":%v,"alias":"normal"}`, datasetID, topK, judge), http.StatusAccepted, &run)
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		var got struct {
			Run e2eRun `json:"run"`
		}
		s.getJSON(t, "/api/v1/evaluations/"+run.ID, http.StatusOK, &got)
		switch got.Run.Status {
		case "done":
			return got.Run
		case "error":
			t.Fatalf("evaluation %s failed: %s", run.ID, got.Run.Error)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("evaluation %s did not finish", run.ID)
	return e2eRun{}
}

func (s *e2eServer) runtimeSearch(t *testing.T, slug, token string, want int) {
	t.Helper()
	data := s.do(t, http.MethodPost, "/runtime/v1/apps/"+slug+"/search", `{"query":"refund within 30 days"}`, token, want)
	if want == http.StatusOK && !strings.Contains(string(data), "refund.md") {
		t.Fatalf("runtime search body does not include refund.md: %s", data)
	}
}

func (s *e2eServer) ragPromptID(t *testing.T) string {
	t.Helper()
	var prompts []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	s.getJSON(t, "/api/v1/prompts", http.StatusOK, &prompts)
	for _, p := range prompts {
		if p.Name == "rag_system" {
			return p.ID
		}
	}
	t.Fatalf("rag_system prompt not found in %+v", prompts)
	return ""
}
