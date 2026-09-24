package usecase

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/sibukixxx/rag-poc/internal/domain/audit"
	"github.com/sibukixxx/rag-poc/internal/domain/oauth"
	"github.com/sibukixxx/rag-poc/internal/domain/secret"
	"github.com/sibukixxx/rag-poc/internal/domain/source"
)

const oauthStateTTL = 10 * time.Minute

// BackgroundScheduler runs source work outside the HTTP request, so a
// sync keeps going after the operator closes the browser.
type BackgroundScheduler interface {
	Enqueue(jobID string)
	Submit(fn func(ctx context.Context))
}

// SyncRef tells the caller how to follow a started sync.
type SyncRef struct {
	Kind string `json:"kind"` // "ingestion_job" (filesystem) or "sync" (API connectors)
	ID   string `json:"id,omitempty"`
}

// SourceControlUseCase is the Web control plane for source connections
// (#31): the browser configures, authorizes, inspects, and controls; the
// server keeps credentials and does the syncing.
type SourceControlUseCase struct {
	Sources    source.Store
	States     oauth.StateStore
	Secrets    secret.Store
	Exchanger  oauth.Exchanger
	Connectors source.Registry
	Providers  map[string]oauth.Provider

	Sync      *SourceSyncUseCase
	Bulk      *BulkIngestUseCase
	Scheduler BackgroundScheduler
	Audit     audit.Recorder
	Now       func() time.Time

	refreshMu sync.Mutex
}

func NewSourceControlUseCase(sources source.Store, states oauth.StateStore, secrets secret.Store, ex oauth.Exchanger,
	connectors source.Registry, providers map[string]oauth.Provider) *SourceControlUseCase {
	return &SourceControlUseCase{
		Sources: sources, States: states, Secrets: secrets, Exchanger: ex,
		Connectors: connectors, Providers: providers, Now: time.Now,
	}
}

// oauthConfig is kept in config_json next to the connector's own scope
// settings; it names the provider, never a credential.
type oauthConfig struct {
	OAuthProvider string          `json:"oauth_provider"`
	Scope         json.RawMessage `json:"scope,omitempty"`
}

var credentialKey = regexp.MustCompile(`(?i)(token|secret|password|passwd|api[_-]?key|credential)`)

// findCredentialKey reports the first object key that looks like a secret.
func findCredentialKey(v any) string {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if credentialKey.MatchString(k) {
				return k
			}
			if found := findCredentialKey(val); found != "" {
				return found
			}
		}
	case []any:
		for _, val := range t {
			if found := findCredentialKey(val); found != "" {
				return found
			}
		}
	}
	return ""
}

func (u *SourceControlUseCase) provider(name string) (oauth.Provider, error) {
	p, ok := u.Providers[name]
	if !ok {
		return oauth.Provider{}, invalidSourceError{fmt.Errorf("oauth provider %q is not configured", name)}
	}
	return p, nil
}

// CreateOAuthConnection registers a connector that needs an account's
// consent. It starts in authorization_required; no data is read until an
// operator authorizes it.
func (u *SourceControlUseCase) CreateOAuthConnection(ctx context.Context, knowledgeBaseID, connectorName, name, oauthProvider string, scope json.RawMessage) (*source.Connection, error) {
	connector, ok := u.Connectors.Get(connectorName)
	if !ok {
		return nil, invalidSourceError{fmt.Errorf("source provider %q is not available", connectorName)}
	}
	if _, ok := connector.(source.CredentialedConnector); !ok {
		return nil, invalidSourceError{fmt.Errorf("source provider %q does not use OAuth", connectorName)}
	}
	if _, err := u.provider(oauthProvider); err != nil {
		return nil, err
	}
	if len(scope) > 0 {
		var parsed any
		if err := json.Unmarshal(scope, &parsed); err != nil {
			return nil, invalidSourceError{fmt.Errorf("scope must be JSON: %w", err)}
		}
		if key := findCredentialKey(parsed); key != "" {
			return nil, invalidSourceError{fmt.Errorf("config must not contain credentials (field %q); credentials are stored in the Secret Store", key)}
		}
	}
	raw, err := json.Marshal(oauthConfig{OAuthProvider: oauthProvider, Scope: scope})
	if err != nil {
		return nil, err
	}
	now := u.Now()
	conn := source.Connection{
		ID: uuid.NewString(), KnowledgeBaseID: knowledgeBaseID, Provider: connector.Provider(), Name: name,
		Config: raw, Enabled: true, AuthState: source.AuthAuthorizationRequired, CreatedAt: now, UpdatedAt: now,
	}
	if err := u.Sources.CreateConnection(ctx, conn); err != nil {
		return nil, err
	}
	recordAudit(ctx, u.Audit, audit.ActionSourceConnectionCreate, audit.OutcomeSuccess, "source_connection:"+conn.ID,
		map[string]string{"provider": conn.Provider, "oauth_provider": oauthProvider, "knowledge_base_id": knowledgeBaseID})
	return &conn, nil
}

func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func hashState(state string) string {
	sum := sha256.Sum256([]byte(state))
	return hex.EncodeToString(sum[:])
}

func (u *SourceControlUseCase) connectionOAuth(ctx context.Context, connectionID string) (*source.Connection, oauth.Provider, error) {
	conn, err := u.Sources.GetConnection(ctx, connectionID)
	if err != nil {
		return nil, oauth.Provider{}, err
	}
	var cfg oauthConfig
	if err := json.Unmarshal(conn.Config, &cfg); err != nil || cfg.OAuthProvider == "" {
		return nil, oauth.Provider{}, invalidSourceError{fmt.Errorf("source connection %s does not use OAuth", connectionID)}
	}
	p, err := u.provider(cfg.OAuthProvider)
	return conn, p, err
}

// StartAuthorization returns the provider URL the browser should open.
// The state is random, single-use, expires after ten minutes, and only its
// hash is stored; PKCE (S256) binds the code to this server.
func (u *SourceControlUseCase) StartAuthorization(ctx context.Context, connectionID string) (string, error) {
	conn, p, err := u.connectionOAuth(ctx, connectionID)
	if err != nil {
		return "", err
	}
	state, err := randomToken()
	if err != nil {
		return "", err
	}
	pending := oauth.PendingAuthorization{StateHash: hashState(state), ConnectionID: conn.ID, Provider: p.Name, ExpiresAt: u.Now().Add(oauthStateTTL)}
	q := url.Values{
		"response_type": {"code"}, "client_id": {p.ClientID}, "redirect_uri": {p.RedirectURL}, "state": {state},
	}
	if len(p.Scopes) > 0 {
		q.Set("scope", strings.Join(p.Scopes, " "))
	}
	if p.PKCE {
		verifier, err := randomToken()
		if err != nil {
			return "", err
		}
		sum := sha256.Sum256([]byte(verifier))
		q.Set("code_challenge", base64.RawURLEncoding.EncodeToString(sum[:]))
		q.Set("code_challenge_method", "S256")
		pending.CodeVerifier = verifier
	}
	if err := u.States.SavePending(ctx, pending); err != nil {
		return "", err
	}
	sep := "?"
	if strings.Contains(p.AuthorizationURL, "?") {
		sep = "&"
	}
	return p.AuthorizationURL + sep + q.Encode(), nil
}

func secretNameFor(connectionID string) string { return "source-oauth:" + connectionID }

// CompleteAuthorization handles the provider callback: it validates and
// consumes the state, exchanges the code, and stores the grant encrypted in
// the Secret Store. config_json and source tables never see the token.
func (u *SourceControlUseCase) CompleteAuthorization(ctx context.Context, state, code, providerError string) (*source.Connection, error) {
	pending, err := u.States.TakePending(ctx, hashState(state))
	if err != nil {
		recordAudit(ctx, u.Audit, audit.ActionSourceAuthorize, audit.OutcomeDenied, "", map[string]string{"reason": "invalid_state"})
		return nil, err
	}
	target := "source_connection:" + pending.ConnectionID
	if !u.Now().Before(pending.ExpiresAt) {
		recordAudit(ctx, u.Audit, audit.ActionSourceAuthorize, audit.OutcomeDenied, target, map[string]string{"reason": "expired_state"})
		return nil, oauth.ErrInvalidState
	}
	if providerError != "" {
		recordAudit(ctx, u.Audit, audit.ActionSourceAuthorize, audit.OutcomeDenied, target, map[string]string{"reason": "provider_denied"})
		return nil, fmt.Errorf("authorization was not granted: %s", truncateForAudit(providerError))
	}
	if code == "" {
		return nil, oauth.ErrInvalidState
	}
	conn, p, err := u.connectionOAuth(ctx, pending.ConnectionID)
	if err != nil {
		return nil, err
	}
	tok, err := u.Exchanger.Exchange(ctx, p, code, pending.CodeVerifier)
	if err != nil {
		recordAudit(ctx, u.Audit, audit.ActionSourceAuthorize, audit.OutcomeFailure, target, nil)
		return nil, err
	}
	if err := u.storeToken(ctx, conn.ID, tok); err != nil {
		return nil, err
	}
	if err := u.Sources.UpdateConnectionAuth(ctx, conn.ID, source.AuthAuthorized, secretNameFor(conn.ID), "", u.Now()); err != nil {
		return nil, err
	}
	recordAudit(ctx, u.Audit, audit.ActionSourceAuthorize, audit.OutcomeSuccess, target, map[string]string{"oauth_provider": p.Name})
	return u.Sources.GetConnection(ctx, conn.ID)
}

func (u *SourceControlUseCase) storeToken(ctx context.Context, connectionID string, tok oauth.Token) error {
	raw, err := json.Marshal(tok)
	if err != nil {
		return err
	}
	if u.Secrets == nil {
		return errors.New("secret store unavailable: set the master key to store OAuth grants")
	}
	return u.Secrets.Set(ctx, secretNameFor(connectionID), raw)
}

type connectionCredentials struct {
	uc   *SourceControlUseCase
	conn source.Connection
}

// Credentials returns an access-token source for an authorized connection.
func (u *SourceControlUseCase) Credentials(conn source.Connection) source.Credentials {
	return connectionCredentials{uc: u, conn: conn}
}

const reauthorizeMessage = "authorization expired or was revoked; authorize the connection again"

func (c connectionCredentials) AccessToken(ctx context.Context) (string, error) {
	u := c.uc
	if c.conn.AuthState != source.AuthAuthorized || c.conn.SecretName == "" || u.Secrets == nil {
		return "", fmt.Errorf("%w: connection is not authorized", oauth.ErrReauthorizationRequired)
	}
	u.refreshMu.Lock()
	defer u.refreshMu.Unlock()
	raw, err := u.Secrets.Get(ctx, c.conn.SecretName)
	if err != nil {
		return "", fmt.Errorf("%w: stored grant is missing", oauth.ErrReauthorizationRequired)
	}
	var tok oauth.Token
	if err := json.Unmarshal(raw, &tok); err != nil {
		return "", fmt.Errorf("decoding stored grant: %w", err)
	}
	if !tok.Expired(u.Now()) {
		return tok.AccessToken, nil
	}
	if tok.RefreshToken == "" {
		return "", u.markReauthorization(ctx, c.conn.ID)
	}
	_, p, err := u.connectionOAuth(ctx, c.conn.ID)
	if err != nil {
		return "", err
	}
	refreshed, err := u.Exchanger.Refresh(ctx, p, tok.RefreshToken)
	if errors.Is(err, oauth.ErrReauthorizationRequired) {
		return "", u.markReauthorization(ctx, c.conn.ID)
	}
	if err != nil {
		return "", err
	}
	if err := u.storeToken(ctx, c.conn.ID, refreshed); err != nil {
		return "", err
	}
	return refreshed.AccessToken, nil
}

func (u *SourceControlUseCase) markReauthorization(ctx context.Context, connectionID string) error {
	_ = u.Sources.UpdateConnectionAuth(context.WithoutCancel(ctx), connectionID, source.AuthReauthorizationRequired,
		secretNameFor(connectionID), reauthorizeMessage, u.Now())
	recordAudit(ctx, u.Audit, audit.ActionSourceAuthorize, audit.OutcomeFailure, "source_connection:"+connectionID,
		map[string]string{"reason": "reauthorization_required"})
	return fmt.Errorf("%w: %s", oauth.ErrReauthorizationRequired, reauthorizeMessage)
}

// SetEnabled pauses or resumes a connection without touching its data.
func (u *SourceControlUseCase) SetEnabled(ctx context.Context, connectionID string, enabled bool) error {
	if err := u.Sources.SetConnectionEnabled(ctx, connectionID, enabled, u.Now()); err != nil {
		return err
	}
	request := "disable"
	if enabled {
		request = "enable"
	}
	recordAudit(ctx, u.Audit, audit.ActionSourceConnectionControl, audit.OutcomeSuccess, "source_connection:"+connectionID, map[string]string{"request": request})
	return nil
}

// Disconnect removes a connection, the documents it synced, and its stored
// credentials. Original data at the provider is untouched.
func (u *SourceControlUseCase) Disconnect(ctx context.Context, connectionID string) (int, error) {
	conn, err := u.Sources.GetConnection(ctx, connectionID)
	if err != nil {
		return 0, err
	}
	removed, err := u.Sources.DeleteConnection(ctx, connectionID)
	if err != nil {
		return 0, err
	}
	if conn.SecretName != "" && u.Secrets != nil {
		if err := u.Secrets.Delete(ctx, conn.SecretName); err != nil {
			return removed, fmt.Errorf("deleting stored credentials: %w", err)
		}
	}
	recordAudit(ctx, u.Audit, audit.ActionSourceConnectionControl, audit.OutcomeSuccess, "source_connection:"+connectionID,
		map[string]string{"request": "disconnect", "documents_removed": fmt.Sprint(removed)})
	return removed, nil
}

// SyncNow starts a sync in the background: a bulk job for filesystem
// sources, a cursor sync for API connectors.
func (u *SourceControlUseCase) SyncNow(ctx context.Context, connectionID string) (SyncRef, error) {
	conn, err := u.Sources.GetConnection(ctx, connectionID)
	if err != nil {
		return SyncRef{}, err
	}
	if !conn.Enabled {
		return SyncRef{}, invalidSourceError{fmt.Errorf("source connection %s is disabled", connectionID)}
	}
	if conn.Provider == FilesystemProvider {
		if u.Bulk == nil {
			return SyncRef{}, errors.New("filesystem ingestion is not configured")
		}
		job, err := u.Bulk.StartJob(ctx, connectionID)
		if err != nil {
			return SyncRef{}, err
		}
		u.Scheduler.Enqueue(job.ID)
		return SyncRef{Kind: "ingestion_job", ID: job.ID}, nil
	}
	switch conn.AuthState {
	case source.AuthAuthorizationRequired, source.AuthReauthorizationRequired:
		return SyncRef{}, fmt.Errorf("%w: authorize the connection before syncing", ErrJobStateConflict)
	}
	if u.Sync == nil {
		return SyncRef{}, errors.New("source sync is not configured")
	}
	recordAudit(ctx, u.Audit, audit.ActionSourceConnectionControl, audit.OutcomeSuccess, "source_connection:"+connectionID, map[string]string{"request": "sync"})
	u.Scheduler.Submit(func(bg context.Context) {
		if _, err := u.Sync.Sync(bg, connectionID); err != nil {
			_ = u.recordSyncError(bg, connectionID, err)
		}
	})
	return SyncRef{Kind: "sync"}, nil
}

func (u *SourceControlUseCase) recordSyncError(ctx context.Context, connectionID string, syncErr error) error {
	conn, err := u.Sources.GetConnection(ctx, connectionID)
	if err != nil {
		return err
	}
	if conn.AuthState == source.AuthReauthorizationRequired {
		return nil // already carries the actionable message
	}
	return u.Sources.UpdateConnectionAuth(ctx, connectionID, conn.AuthState, conn.SecretName, truncateForAudit(syncErr.Error()), u.Now())
}

// ConnectionView is what the control plane shows for one connection. It
// carries scope and state, never credentials.
type ConnectionView struct {
	ID              string                  `json:"id"`
	KnowledgeBaseID string                  `json:"knowledge_base_id"`
	Provider        string                  `json:"provider"`
	Name            string                  `json:"name"`
	Enabled         bool                    `json:"enabled"`
	AuthState       source.AuthState        `json:"auth_state"`
	LastError       string                  `json:"last_error,omitempty"`
	OAuthProvider   string                  `json:"oauth_provider,omitempty"`
	Scope           json.RawMessage         `json:"scope,omitempty"`
	Filesystem      *FilesystemSourceConfig `json:"filesystem,omitempty"`
	CreatedAt       time.Time               `json:"created_at"`
	UpdatedAt       time.Time               `json:"updated_at"`
	LatestJob       *JobProgress            `json:"latest_job,omitempty"`
	LatestSync      *source.SyncJob         `json:"latest_sync,omitempty"`
}

// ListConnections returns a knowledge base's connections with their state
// and most recent sync evidence.
func (u *SourceControlUseCase) ListConnections(ctx context.Context, knowledgeBaseID string) ([]ConnectionView, error) {
	conns, err := u.Sources.ListConnections(ctx, knowledgeBaseID)
	if err != nil {
		return nil, err
	}
	out := make([]ConnectionView, 0, len(conns))
	for _, c := range conns {
		v := ConnectionView{
			ID: c.ID, KnowledgeBaseID: c.KnowledgeBaseID, Provider: c.Provider, Name: c.Name, Enabled: c.Enabled,
			AuthState: c.AuthState, LastError: c.LastError, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
		}
		if c.Provider == FilesystemProvider {
			var cfg FilesystemSourceConfig
			if json.Unmarshal(c.Config, &cfg) == nil {
				v.Filesystem = &cfg
			}
			if u.Bulk != nil {
				jobs, err := u.Bulk.Jobs.ListJobs(ctx, c.ID, 1)
				if err != nil {
					return nil, err
				}
				if len(jobs) == 1 {
					p, err := u.Bulk.Progress(ctx, jobs[0].ID)
					if err != nil {
						return nil, err
					}
					v.LatestJob = &p
				}
			}
		} else {
			var cfg oauthConfig
			if json.Unmarshal(c.Config, &cfg) == nil {
				v.OAuthProvider, v.Scope = cfg.OAuthProvider, cfg.Scope
			}
			job, err := u.Sources.LatestJob(ctx, c.ID)
			switch {
			case err == nil:
				v.LatestSync = job
			case !errors.Is(err, source.ErrNotFound):
				return nil, err
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// Catalog describes what this server can connect to, so the UI only offers
// sources that actually work here.
type Catalog struct {
	FilesystemEnabled bool     `json:"filesystem_enabled"`
	AllowedRoots      []string `json:"allowed_roots"`
	OAuthConnectors   []string `json:"oauth_connectors"`
	OAuthProviders    []string `json:"oauth_providers"`
}

func (u *SourceControlUseCase) Catalog(oauthConnectors []string) Catalog {
	c := Catalog{AllowedRoots: []string{}, OAuthConnectors: oauthConnectors, OAuthProviders: []string{}}
	if c.OAuthConnectors == nil {
		c.OAuthConnectors = []string{}
	}
	if u.Bulk != nil && len(u.Bulk.AllowedRoots) > 0 {
		c.FilesystemEnabled = true
		c.AllowedRoots = append(c.AllowedRoots, u.Bulk.AllowedRoots...)
	}
	for name := range u.Providers {
		c.OAuthProviders = append(c.OAuthProviders, name)
	}
	sort.Strings(c.OAuthProviders)
	return c
}
