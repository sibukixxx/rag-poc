package oauthhttp_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sibukixxx/rag-poc/internal/adapter/oauthhttp"
	"github.com/sibukixxx/rag-poc/internal/domain/oauth"
)

func tokenServer(t *testing.T, handle func(w http.ResponseWriter, r *http.Request)) oauth.Provider {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(handle))
	t.Cleanup(srv.Close)
	return oauth.Provider{Name: "fake", TokenURL: srv.URL + "/token", ClientID: "client-1", ClientSecret: "client-secret", RedirectURL: "https://forgeai.example/oauth/callback"}
}

func TestExchangeSendsCodeVerifierAndParsesToken(t *testing.T) {
	var form map[string]string
	p := tokenServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		form = map[string]string{}
		for k := range r.PostForm {
			form[k] = r.PostForm.Get(k)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"at-1","refresh_token":"rt-1","token_type":"Bearer","expires_in":3600}`))
	})
	now := time.Now()

	tok, err := oauthhttp.Client{Now: func() time.Time { return now }}.Exchange(context.Background(), p, "code-1", "verifier-1")

	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"grant_type": "authorization_code", "code": "code-1", "code_verifier": "verifier-1",
		"redirect_uri": p.RedirectURL, "client_id": "client-1", "client_secret": "client-secret"}
	for k, v := range want {
		if form[k] != v {
			t.Fatalf("form[%s] = %q, want %q (form %v)", k, form[k], v, form)
		}
	}
	if tok.AccessToken != "at-1" || tok.RefreshToken != "rt-1" || !tok.Expiry.Equal(now.Add(time.Hour)) {
		t.Fatalf("token = %+v", tok)
	}
}

func TestRefreshInvalidGrantMeansReauthorizationRequired(t *testing.T) {
	p := tokenServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"token revoked for rt-secret"}`))
	})

	_, err := oauthhttp.Client{}.Refresh(context.Background(), p, "rt-secret")

	if !errors.Is(err, oauth.ErrReauthorizationRequired) {
		t.Fatalf("error = %v, want ErrReauthorizationRequired", err)
	}
	if err.Error() != "oauth: reauthorization required: provider returned invalid_grant" {
		t.Fatalf("error text = %q (must not echo provider descriptions or tokens)", err.Error())
	}
}

func TestExchangeServerErrorIsNotAReauthorization(t *testing.T) {
	p := tokenServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})

	_, err := oauthhttp.Client{}.Exchange(context.Background(), p, "c", "v")

	if err == nil || errors.Is(err, oauth.ErrReauthorizationRequired) || err.Error() != "oauth: token endpoint returned HTTP 502" {
		t.Fatalf("error = %v", err)
	}
}
