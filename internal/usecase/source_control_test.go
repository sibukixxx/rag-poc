package usecase_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sourceadapter "github.com/sibukixxx/rag-poc/internal/adapter/source"
	"github.com/sibukixxx/rag-poc/internal/adapter/sqlite"
	"github.com/sibukixxx/rag-poc/internal/domain/oauth"
	"github.com/sibukixxx/rag-poc/internal/domain/source"
	"github.com/sibukixxx/rag-poc/internal/usecase"
)

// memorySecrets is a test double for the encrypted Secret Store.
type memorySecrets struct{ values map[string][]byte }

func (m *memorySecrets) Set(_ context.Context, name string, v []byte) error {
	m.values[name] = append([]byte(nil), v...)
	return nil
}
func (m *memorySecrets) Get(_ context.Context, name string) ([]byte, error) {
	v, ok := m.values[name]
	if !ok {
		return nil, errors.New("not found")
	}
	return v, nil
}
func (m *memorySecrets) Delete(_ context.Context, name string) error {
	delete(m.values, name)
	return nil
}

// scriptedExchanger returns fixed tokens and records what it received.
type scriptedExchanger struct {
	exchangeCode, verifier string
	refreshErr             error
	refreshes              int
	now                    time.Time
}

func (s *scriptedExchanger) Exchange(_ context.Context, _ oauth.Provider, code, verifier string) (oauth.Token, error) {
	s.exchangeCode, s.verifier = code, verifier
	return oauth.Token{AccessToken: "access-1", RefreshToken: "refresh-1", Expiry: s.now.Add(time.Hour)}, nil
}
func (s *scriptedExchanger) Refresh(context.Context, oauth.Provider, string) (oauth.Token, error) {
	s.refreshes++
	if s.refreshErr != nil {
		return oauth.Token{}, s.refreshErr
	}
	return oauth.Token{AccessToken: "access-2", RefreshToken: "refresh-1", Expiry: s.now.Add(2 * time.Hour)}, nil
}

type credentialedStub struct{}

func (credentialedStub) Provider() string { return "fake_feed" }
func (credentialedStub) Pull(context.Context, source.Connection, string) (source.Batch, error) {
	return source.Batch{}, errors.New("credentials required")
}
func (credentialedStub) PullWithCredentials(ctx context.Context, _ source.Connection, _ string, creds source.Credentials) (source.Batch, error) {
	if _, err := creds.AccessToken(ctx); err != nil {
		return source.Batch{}, err
	}
	return source.Batch{}, nil
}

type controlFixture struct {
	uc      *usecase.SourceControlUseCase
	store   *sqlite.SourceStore
	secrets *memorySecrets
	ex      *scriptedExchanger
	kbID    string
	now     time.Time
}

func newControlFixture(t *testing.T) controlFixture {
	t.Helper()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "forgeai.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	kb, err := sqlite.NewKnowledgeStore(db).EnsureKnowledgeBase(context.Background(), "K", "k")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	ex := &scriptedExchanger{now: now}
	secrets := &memorySecrets{values: map[string][]byte{}}
	store := sqlite.NewSourceStore(db)
	uc := usecase.NewSourceControlUseCase(store, sqlite.NewOAuthStateStore(db), secrets, ex,
		sourceadapter.NewRegistry(credentialedStub{}),
		map[string]oauth.Provider{"fakeidp": {
			Name: "fakeidp", AuthorizationURL: "https://idp.example/authorize", TokenURL: "https://idp.example/token",
			ClientID: "client-1", Scopes: []string{"read"}, RedirectURL: "https://forgeai.example/oauth/callback", PKCE: true,
		}})
	uc.Now = func() time.Time { return now }
	return controlFixture{uc: uc, store: store, secrets: secrets, ex: ex, kbID: kb.ID, now: now}
}

func (f controlFixture) authorize(t *testing.T) (*source.Connection, string) {
	t.Helper()
	ctx := context.Background()
	conn, err := f.uc.CreateOAuthConnection(ctx, f.kbID, "fake_feed", "feed", "fakeidp", json.RawMessage(`{"projects":["ops"]}`))
	if err != nil {
		t.Fatal(err)
	}
	authURL, err := f.uc.StartAuthorization(ctx, conn.ID)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	return conn, u.Query().Get("state")
}

func TestStartAuthorizationBuildsPKCERequestForTheConfiguredRedirect(t *testing.T) {
	f := newControlFixture(t)
	conn, err := f.uc.CreateOAuthConnection(context.Background(), f.kbID, "fake_feed", "feed", "fakeidp", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}

	authURL, err := f.uc.StartAuthorization(context.Background(), conn.ID)

	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(authURL)
	q := u.Query()
	if u.Host != "idp.example" || q.Get("response_type") != "code" || q.Get("client_id") != "client-1" ||
		q.Get("redirect_uri") != "https://forgeai.example/oauth/callback" || q.Get("scope") != "read" ||
		q.Get("code_challenge_method") != "S256" || len(q.Get("code_challenge")) != 43 || len(q.Get("state")) < 32 {
		t.Fatalf("authorization URL = %s", authURL)
	}
	if conn.AuthState != source.AuthAuthorizationRequired {
		t.Fatalf("new connection auth state = %q", conn.AuthState)
	}
}

func TestCompleteAuthorizationStoresTokenInSecretStoreOnly(t *testing.T) {
	f := newControlFixture(t)
	conn, state := f.authorize(t)

	got, err := f.uc.CompleteAuthorization(context.Background(), state, "code-xyz", "")

	if err != nil {
		t.Fatal(err)
	}
	if got.AuthState != source.AuthAuthorized || got.SecretName == "" || strings.Contains(string(got.Config), "access-1") {
		t.Fatalf("connection = %+v", got)
	}
	if f.ex.exchangeCode != "code-xyz" || f.ex.verifier == "" {
		t.Fatalf("exchange received code=%q verifier=%q", f.ex.exchangeCode, f.ex.verifier)
	}
	var tok oauth.Token
	if err := json.Unmarshal(f.secrets.values[got.SecretName], &tok); err != nil || tok.AccessToken != "access-1" {
		t.Fatalf("secret %q = %s", got.SecretName, f.secrets.values[got.SecretName])
	}
	// The same state cannot be replayed.
	if _, err := f.uc.CompleteAuthorization(context.Background(), state, "code-xyz", ""); !errors.Is(err, oauth.ErrInvalidState) {
		t.Fatalf("replayed state error = %v", err)
	}
	_ = conn
}

func TestCompleteAuthorizationRejectsUnknownExpiredAndDeniedConsent(t *testing.T) {
	f := newControlFixture(t)
	_, state := f.authorize(t)

	if _, err := f.uc.CompleteAuthorization(context.Background(), "not-a-state", "c", ""); !errors.Is(err, oauth.ErrInvalidState) {
		t.Fatalf("unknown state error = %v", err)
	}
	later := f.now.Add(11 * time.Minute)
	f.uc.Now = func() time.Time { return later }
	if _, err := f.uc.CompleteAuthorization(context.Background(), state, "c", ""); !errors.Is(err, oauth.ErrInvalidState) {
		t.Fatalf("expired state error = %v", err)
	}
	f.uc.Now = func() time.Time { return f.now }
	_, state2 := f.authorize(t)
	if _, err := f.uc.CompleteAuthorization(context.Background(), state2, "", "access_denied"); err == nil || err.Error() != "authorization was not granted: access_denied" {
		t.Fatalf("denied consent error = %v", err)
	}
}

func TestCredentialsRefreshExpiredTokensAndFlagRevokedGrants(t *testing.T) {
	f := newControlFixture(t)
	_, state := f.authorize(t)
	conn, err := f.uc.CompleteAuthorization(context.Background(), state, "code", "")
	if err != nil {
		t.Fatal(err)
	}
	f.uc.Now = func() time.Time { return f.now.Add(90 * time.Minute) } // access token expired

	tok, err := f.uc.Credentials(*conn).AccessToken(context.Background())
	if err != nil || tok != "access-2" || f.ex.refreshes != 1 {
		t.Fatalf("refreshed token = %q, %v (refreshes %d)", tok, err, f.ex.refreshes)
	}

	f.uc.Now = func() time.Time { return f.now.Add(5 * time.Hour) }
	f.ex.refreshErr = oauth.ErrReauthorizationRequired
	_, err = f.uc.Credentials(*conn).AccessToken(context.Background())

	if !errors.Is(err, oauth.ErrReauthorizationRequired) {
		t.Fatalf("revoked grant error = %v", err)
	}
	after, _ := f.store.GetConnection(context.Background(), conn.ID)
	if after.AuthState != source.AuthReauthorizationRequired || after.LastError != "authorization expired or was revoked; authorize the connection again" {
		t.Fatalf("connection after revocation = %+v", after)
	}
}

func TestCreateOAuthConnectionRejectsSecretsInConfigAndUnknownProviders(t *testing.T) {
	f := newControlFixture(t)
	ctx := context.Background()

	_, err := f.uc.CreateOAuthConnection(ctx, f.kbID, "fake_feed", "feed", "fakeidp", json.RawMessage(`{"projects":["a"],"api_token":"x"}`))
	if !errors.Is(err, usecase.ErrInvalidSourceConfig) || err.Error() != `config must not contain credentials (field "api_token"); credentials are stored in the Secret Store` {
		t.Fatalf("secret-in-config error = %v", err)
	}
	if _, err := f.uc.CreateOAuthConnection(ctx, f.kbID, "fake_feed", "feed", "nope", nil); !errors.Is(err, usecase.ErrInvalidSourceConfig) {
		t.Fatalf("unknown oauth provider error = %v", err)
	}
	if _, err := f.uc.CreateOAuthConnection(ctx, f.kbID, "no_such_connector", "feed", "fakeidp", nil); !errors.Is(err, usecase.ErrInvalidSourceConfig) {
		t.Fatalf("unknown connector error = %v", err)
	}
}

func TestDisconnectDeletesConnectionAndCredentials(t *testing.T) {
	f := newControlFixture(t)
	_, state := f.authorize(t)
	conn, err := f.uc.CompleteAuthorization(context.Background(), state, "code", "")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := f.uc.Disconnect(context.Background(), conn.ID); err != nil {
		t.Fatal(err)
	}

	if _, ok := f.secrets.values[conn.SecretName]; ok {
		t.Fatal("OAuth grant still in the Secret Store after disconnect")
	}
	if _, err := f.store.GetConnection(context.Background(), conn.ID); !errors.Is(err, source.ErrNotFound) {
		t.Fatalf("connection after disconnect: %v", err)
	}
}
