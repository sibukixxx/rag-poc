package app

import (
	"context"
	"os"

	"github.com/sibukixxx/rag-poc/internal/adapter/oauthhttp"
	sourceadapter "github.com/sibukixxx/rag-poc/internal/adapter/source"
	"github.com/sibukixxx/rag-poc/internal/adapter/sqlite"
	"github.com/sibukixxx/rag-poc/internal/domain/oauth"
	"github.com/sibukixxx/rag-poc/internal/domain/secret"
	"github.com/sibukixxx/rag-poc/internal/domain/source"
	"github.com/sibukixxx/rag-poc/internal/usecase"
)

// oauthProviders resolves configured authorization servers. Client secrets
// come from an environment variable or the Secret Store, never the YAML.
func (a *App) oauthProviders(secrets secret.Store) map[string]oauth.Provider {
	out := make(map[string]oauth.Provider, len(a.Config.Sources.OAuthProviders))
	for name, p := range a.Config.Sources.OAuthProviders {
		clientSecret := ""
		if p.ClientSecretEnv != "" {
			clientSecret = os.Getenv(p.ClientSecretEnv)
		}
		if clientSecret == "" && p.ClientSecretSecret != "" && secrets != nil {
			if v, err := secrets.Get(context.Background(), p.ClientSecretSecret); err == nil {
				clientSecret = string(v)
			}
		}
		out[name] = oauth.Provider{
			Name: name, AuthorizationURL: p.AuthorizationURL, TokenURL: p.TokenURL, ClientID: p.ClientID,
			ClientSecret: clientSecret, Scopes: p.Scopes, RedirectURL: p.RedirectURL, PKCE: p.PKCEEnabled(),
		}
	}
	return out
}

// SourceControl wires the Web source control plane (#31) on top of the
// shared background runner.
func (a *App) SourceControl() (*usecase.SourceControlUseCase, error) {
	runner, err := a.jobs()
	if err != nil {
		return nil, err
	}
	ingest, err := a.Ingest()
	if err != nil {
		return nil, err
	}
	secrets, _ := a.Secrets()
	store := sqlite.NewSourceStore(a.DB)
	registry := sourceadapter.NewRegistry(a.Connectors...)
	uc := usecase.NewSourceControlUseCase(store, sqlite.NewOAuthStateStore(a.DB), secrets, oauthhttp.Client{}, registry, a.oauthProviders(secrets))
	sync := usecase.NewSourceSyncUseCase(store, registry, ingest)
	sync.Credentials = uc.Credentials
	uc.Sync = sync
	uc.Bulk = runner.uc
	uc.Scheduler = runner
	uc.Audit = a.Audit()
	return uc, nil
}

// oauthConnectorNames lists registered connectors that use OAuth.
func (a *App) oauthConnectorNames() []string {
	var names []string
	for _, c := range a.Connectors {
		if _, ok := c.(source.CredentialedConnector); ok {
			names = append(names, c.Provider())
		}
	}
	return names
}
