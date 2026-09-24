package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sibukixxx/rag-poc/internal/domain/source"
)

const (
	liveAccessToken  = "AT-live-123"
	liveRefreshToken = "RT-live-456"
	idpClientSecret  = "idp-client-s3cret"
)

// fakeIdP is an OAuth authorization server plus a protected data API.
type fakeIdP struct {
	srv       *httptest.Server
	mu        sync.Mutex
	challenge string
	revoked   bool
}

func newFakeIdP(t *testing.T) *fakeIdP {
	t.Helper()
	idp := &fakeIdP{}
	mux := http.NewServeMux()
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("client_id") != "forgeai-client" || q.Get("code_challenge_method") != "S256" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		idp.mu.Lock()
		idp.challenge = q.Get("code_challenge")
		idp.mu.Unlock()
		back := q.Get("redirect_uri") + "?" + url.Values{"code": {"code-abc"}, "state": {q.Get("state")}}.Encode()
		http.Redirect(w, r, back, http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		idp.mu.Lock()
		defer idp.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.PostForm.Get("client_secret") != idpClientSecret {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
			return
		}
		switch r.PostForm.Get("grant_type") {
		case "authorization_code":
			sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
			if r.PostForm.Get("code") != "code-abc" || base64.RawURLEncoding.EncodeToString(sum[:]) != idp.challenge {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
				return
			}
		case "refresh_token":
			if idp.revoked || r.PostForm.Get("refresh_token") != liveRefreshToken {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
				return
			}
		}
		// A 30s lifetime is inside the refresh skew, so every use refreshes.
		_, _ = fmt.Fprintf(w, `{"access_token":%q,"refresh_token":%q,"expires_in":30}`, liveAccessToken, liveRefreshToken)
	})
	mux.HandleFunc("/data", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+liveAccessToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`[{"id":"runbook-1","title":"Incident runbook","body":"Escalate Sev1 incidents to the on-call lead within 15 minutes."}]`))
	})
	idp.srv = httptest.NewServer(mux)
	t.Cleanup(idp.srv.Close)
	return idp
}

func (idp *fakeIdP) revoke() {
	idp.mu.Lock()
	idp.revoked = true
	idp.mu.Unlock()
}

// fakeFeedConnector reads the IdP's data API with the supplied token.
type fakeFeedConnector struct{ dataURL string }

func (fakeFeedConnector) Provider() string { return "fake_feed" }
func (fakeFeedConnector) Pull(context.Context, source.Connection, string) (source.Batch, error) {
	return source.Batch{}, fmt.Errorf("fake_feed requires credentials")
}
func (c fakeFeedConnector) PullWithCredentials(ctx context.Context, _ source.Connection, _ string, creds source.Credentials) (source.Batch, error) {
	tok, err := creds.AccessToken(ctx)
	if err != nil {
		return source.Batch{}, err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.dataURL, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return source.Batch{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return source.Batch{}, fmt.Errorf("fake_feed: HTTP %d", resp.StatusCode)
	}
	var items []struct{ ID, Title, Body string }
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return source.Batch{}, err
	}
	var batch source.Batch
	for _, it := range items {
		batch.Documents = append(batch.Documents, source.Document{ExternalID: it.ID, Title: it.Title, Body: it.Body})
	}
	return batch, nil
}

type oauthEnv struct {
	idp        *fakeIdP
	configPath string
	listener   net.Listener
	connector  source.Connector
}

func newOAuthEnv(t *testing.T) *oauthEnv {
	t.Helper()
	provider := newFakeProvider(t, 64)
	idp := newFakeIdP(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	configPath := writeE2EConfig(t, provider.baseURL())
	extra := fmt.Sprintf(`sources:
  oauth_providers:
    fakeidp:
      authorization_url: %[1]s/authorize
      token_url: %[1]s/token
      client_id: forgeai-client
      client_secret_env: FORGEAI_E2E_IDP_SECRET
      scopes: [docs.read]
      redirect_url: http://%[2]s/oauth/callback
`, idp.srv.URL, ln.Addr().String())
	f, err := os.OpenFile(configPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(extra)
	_ = f.Close()
	t.Setenv("FORGEAI_E2E_PROVIDER_KEY", "fake-key")
	t.Setenv("FORGEAI_E2E_IDP_SECRET", idpClientSecret)
	t.Setenv("FORGEAI_E2E_MASTER_KEY", "MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE=")
	t.Setenv("FORGEAI_DEMO_AUTH_ENABLED", "")
	return &oauthEnv{idp: idp, configPath: configPath, listener: ln, connector: fakeFeedConnector{dataURL: idp.srv.URL + "/data"}}
}

// start boots ForgeAI on the pre-reserved address so the configured
// redirect_url points at it. Each call is a fresh process on the same DB.
func (e *oauthEnv) start(t *testing.T) *e2eServer {
	t.Helper()
	a, err := Bootstrap(e.configPath)
	if err != nil {
		t.Fatal(err)
	}
	a.Connectors = []source.Connector{e.connector}
	h, err := a.Handler()
	if err != nil {
		t.Fatal(err)
	}
	ln := e.listener
	if ln == nil {
		ln, err = net.Listen("tcp", e.addr(t))
		if err != nil {
			t.Fatal(err)
		}
	}
	e.listener = nil
	srv := httptest.NewUnstartedServer(h)
	_ = srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	s := &e2eServer{app: a, http: srv}
	t.Cleanup(s.close)
	return s
}

func (e *oauthEnv) addr(t *testing.T) string {
	t.Helper()
	u, err := url.Parse(strings.TrimSuffix(e.redirect(t), "/oauth/callback"))
	if err != nil {
		t.Fatal(err)
	}
	return u.Host
}

func (e *oauthEnv) redirect(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(e.configPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "redirect_url: "); ok {
			return v
		}
	}
	t.Fatal("redirect_url not in config")
	return ""
}

type connectionView struct {
	ID         string `json:"id"`
	AuthState  string `json:"auth_state"`
	LastError  string `json:"last_error"`
	LatestSync *struct {
		Status  string `json:"status"`
		Created int    `json:"created"`
		Skipped int    `json:"skipped"`
	} `json:"latest_sync"`
}

func listConnections(t *testing.T, srv *e2eServer, kbID string) []connectionView {
	t.Helper()
	var list []connectionView
	srv.getJSON(t, "/api/v1/source-connections?knowledge_base_id="+kbID, http.StatusOK, &list)
	return list
}

func waitForConnection(t *testing.T, srv *e2eServer, kbID string, done func(connectionView) bool) connectionView {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if list := listConnections(t, srv, kbID); len(list) == 1 && done(list[0]) {
			return list[0]
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("connection did not reach the expected state: %+v", listConnections(t, srv, kbID))
	return connectionView{}
}

// assertNoPlaintextTokens scans every table that must not hold credentials.
func assertNoPlaintextTokens(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, table := range []string{"source_connections", "source_items", "source_sync_jobs", "audit_events", "oauth_states", "secrets"} {
		rows, err := db.Query(`SELECT * FROM ` + table)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			for _, v := range vals {
				s := fmt.Sprint(v)
				if b, ok := v.([]byte); ok {
					s = string(b)
				}
				for _, secret := range []string{liveAccessToken, liveRefreshToken, idpClientSecret} {
					if strings.Contains(s, secret) {
						t.Fatalf("table %s holds %q in plaintext", table, secret)
					}
				}
			}
		}
		rows.Close()
	}
}

func TestOAuthSourceFlowFromBrowserConsentToServerSideSync(t *testing.T) {
	env := newOAuthEnv(t)
	srv := env.start(t)
	var kb struct {
		ID string `json:"id"`
	}
	srv.postJSON(t, "/api/v1/knowledge-bases", `{"name":"Ops","slug":"ops"}`, http.StatusOK, &kb)

	// 1. Connect: created, but nothing is read before consent.
	var conn struct {
		ID        string `json:"id"`
		AuthState string `json:"auth_state"`
	}
	srv.postJSON(t, "/api/v1/source-connections",
		fmt.Sprintf(`{"knowledge_base_id":%q,"provider":"fake_feed","oauth_provider":"fakeidp","name":"ops wiki","scope":{"spaces":["OPS"]}}`, kb.ID),
		http.StatusCreated, &conn)
	if conn.AuthState != "authorization_required" {
		t.Fatalf("new connection = %+v", conn)
	}
	srv.do(t, http.MethodPost, "/api/v1/source-connections/"+conn.ID+"/sync", "", "", http.StatusConflict)

	// 2. Authorize in the "browser": provider redirects back to ForgeAI.
	var auth struct {
		AuthorizationURL string `json:"authorization_url"`
	}
	srv.postJSON(t, "/api/v1/source-connections/"+conn.ID+"/authorize", "", http.StatusOK, &auth)
	browser := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := browser.Get(auth.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	callback := resp.Header.Get("Location")
	if resp.StatusCode != http.StatusFound || !strings.HasPrefix(callback, env.redirect(t)+"?") {
		t.Fatalf("provider response %d Location %q", resp.StatusCode, callback)
	}
	resp, err = browser.Get(callback)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(page), "接続が完了しました") {
		t.Fatalf("callback = %d %s", resp.StatusCode, page)
	}
	// The browser is now closed; everything below happens server-side.

	// 3. Sync now runs in the background and indexes the provider's data.
	srv.do(t, http.MethodPost, "/api/v1/source-connections/"+conn.ID+"/sync", "", "", http.StatusAccepted)
	synced := waitForConnection(t, srv, kb.ID, func(c connectionView) bool { return c.LatestSync != nil && c.LatestSync.Status == "completed" })
	if synced.AuthState != "authorized" || synced.LatestSync.Created != 1 {
		t.Fatalf("after sync = %+v", synced)
	}
	var search struct {
		Results []struct {
			Filename string `json:"filename"`
		} `json:"results"`
	}
	srv.postJSON(t, "/api/v1/knowledge-bases/"+kb.ID+"/search", `{"query":"Sev1 on-call","top_k":3}`, http.StatusOK, &search)
	if len(search.Results) == 0 || search.Results[0].Filename != "Incident runbook" {
		t.Fatalf("search = %+v", search.Results)
	}
	assertNoPlaintextTokens(t, srv.app.DB)

	// 4. A restart keeps the authorization through the encrypted secret.
	srv.close()
	srv = env.start(t)
	srv.do(t, http.MethodPost, "/api/v1/source-connections/"+conn.ID+"/sync", "", "", http.StatusAccepted)
	waitForConnection(t, srv, kb.ID, func(c connectionView) bool { return c.LatestSync != nil && c.LatestSync.Skipped == 1 })

	// 5. A forged or replayed callback is rejected.
	resp, err = browser.Get(env.redirect(t) + "?state=forged&code=code-abc")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("forged callback status = %d, want 400", resp.StatusCode)
	}

	// 6. Revocation at the provider becomes an actionable state.
	env.idp.revoke()
	srv.do(t, http.MethodPost, "/api/v1/source-connections/"+conn.ID+"/sync", "", "", http.StatusAccepted)
	revoked := waitForConnection(t, srv, kb.ID, func(c connectionView) bool { return c.AuthState == "reauthorization_required" })
	if revoked.LastError != "authorization expired or was revoked; authorize the connection again" {
		t.Fatalf("revoked connection = %+v", revoked)
	}
	assertNoPlaintextTokens(t, srv.app.DB)

	// 7. Disconnect removes the synced documents and the stored grant.
	var removed struct {
		DocumentsRemoved int `json:"documents_removed"`
	}
	decode(t, srv.do(t, http.MethodDelete, "/api/v1/source-connections/"+conn.ID, "", "", http.StatusOK), &removed)
	if removed.DocumentsRemoved != 1 || len(listConnections(t, srv, kb.ID)) != 0 {
		t.Fatalf("disconnect removed %d documents; connections left %d", removed.DocumentsRemoved, len(listConnections(t, srv, kb.ID)))
	}
	var secrets int
	if err := srv.app.DB.QueryRow(`SELECT COUNT(1) FROM secrets WHERE name LIKE 'source-oauth:%'`).Scan(&secrets); err != nil || secrets != 0 {
		t.Fatalf("stored grants after disconnect = %d (%v)", secrets, err)
	}
}
