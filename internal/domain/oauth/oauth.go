// Package oauth is the provider-neutral OAuth 2.0 authorization-code model
// used by source connectors that need a user's consent (#31). Connectors
// that authenticate differently (filesystem, service accounts, static
// feed tokens) never go through it.
package oauth

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrInvalidState rejects a callback whose state is unknown, expired,
	// or already used.
	ErrInvalidState = errors.New("oauth: invalid or expired state")
	// ErrReauthorizationRequired means the stored grant no longer works
	// (revoked, expired refresh token); an operator must authorize again.
	ErrReauthorizationRequired = errors.New("oauth: reauthorization required")
)

// Provider is an operator-configured authorization server.
type Provider struct {
	Name             string
	AuthorizationURL string
	TokenURL         string
	ClientID         string
	ClientSecret     string
	Scopes           []string
	RedirectURL      string
	PKCE             bool
}

// Token is the grant stored (encrypted) in the Secret Store.
type Token struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenType    string    `json:"token_type,omitempty"`
	Expiry       time.Time `json:"expiry,omitempty"`
}

// Expired reports whether the access token should be refreshed.
func (t Token) Expired(now time.Time) bool {
	return !t.Expiry.IsZero() && !now.Add(time.Minute).Before(t.Expiry)
}

// Exchanger talks to a provider's token endpoint.
type Exchanger interface {
	Exchange(ctx context.Context, p Provider, code, codeVerifier string) (Token, error)
	Refresh(ctx context.Context, p Provider, refreshToken string) (Token, error)
}

// PendingAuthorization is one in-flight consent, keyed by the hash of its
// state so a database reader cannot replay an authorization.
type PendingAuthorization struct {
	StateHash    string
	ConnectionID string
	Provider     string
	CodeVerifier string
	ExpiresAt    time.Time
}

// StateStore persists pending authorizations.
type StateStore interface {
	SavePending(ctx context.Context, p PendingAuthorization) error
	// TakePending returns and deletes the entry (single use).
	TakePending(ctx context.Context, stateHash string) (*PendingAuthorization, error)
}
