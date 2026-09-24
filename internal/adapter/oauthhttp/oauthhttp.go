// Package oauthhttp implements oauth.Exchanger against an RFC 6749 token
// endpoint. Errors never include provider descriptions or token values.
package oauthhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/sibukixxx/rag-poc/internal/domain/oauth"
)

type Client struct {
	HTTP *http.Client
	Now  func() time.Time
}

var _ oauth.Exchanger = Client{}

func (c Client) Exchange(ctx context.Context, p oauth.Provider, code, codeVerifier string) (oauth.Token, error) {
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {p.RedirectURL}}
	if codeVerifier != "" {
		form.Set("code_verifier", codeVerifier)
	}
	return c.post(ctx, p, form, "")
}

func (c Client) Refresh(ctx context.Context, p oauth.Provider, refreshToken string) (oauth.Token, error) {
	return c.post(ctx, p, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}}, refreshToken)
}

func (c Client) post(ctx context.Context, p oauth.Provider, form url.Values, previousRefresh string) (oauth.Token, error) {
	form.Set("client_id", p.ClientID)
	if p.ClientSecret != "" {
		form.Set("client_secret", p.ClientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return oauth.Token{}, fmt.Errorf("oauth: building token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return oauth.Token{}, fmt.Errorf("oauth: token request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	var parsed struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
		Error        string `json:"error"`
	}
	_ = json.Unmarshal(body, &parsed)
	if parsed.Error == "invalid_grant" {
		return oauth.Token{}, fmt.Errorf("%w: provider returned invalid_grant", oauth.ErrReauthorizationRequired)
	}
	if resp.StatusCode != http.StatusOK {
		return oauth.Token{}, fmt.Errorf("oauth: token endpoint returned HTTP %d", resp.StatusCode)
	}
	if parsed.AccessToken == "" {
		return oauth.Token{}, fmt.Errorf("oauth: token endpoint returned no access_token")
	}
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	tok := oauth.Token{AccessToken: parsed.AccessToken, RefreshToken: parsed.RefreshToken, TokenType: parsed.TokenType}
	if tok.RefreshToken == "" {
		tok.RefreshToken = previousRefresh // providers may omit it on refresh
	}
	if parsed.ExpiresIn > 0 {
		tok.Expiry = now().Add(time.Duration(parsed.ExpiresIn) * time.Second)
	}
	return tok, nil
}
