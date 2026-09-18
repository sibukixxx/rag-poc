package egress_test

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/sibukixxx/rag-poc/internal/adapter/egress"
)

func TestPolicyLocalOnlyDestinations(t *testing.T) {
	p := egress.Policy{Mode: egress.ModeLocalOnly, AllowPrivateNetwork: true, AllowedDestinations: []string{"https://llm.corp.example:8443"}}
	for _, u := range []string{"http://localhost:11434/v1", "http://127.0.0.1:8000/v1", "http://10.0.0.4/v1", "https://llm.corp.example:8443/v1"} {
		if err := p.Check(u); err != nil {
			t.Errorf("Check(%q): %v", u, err)
		}
	}
	if err := p.Check("https://api.openai.com/v1"); !errors.Is(err, egress.ErrBlocked) {
		t.Fatalf("public destination error = %v, want ErrBlocked", err)
	}
}

func TestTransportBlocksBeforeTransmission(t *testing.T) {
	base := &countingTransport{}
	client := &http.Client{Transport: egress.Transport{Policy: egress.Policy{Mode: egress.ModeLocalOnly}, Base: base}}
	resp, err := client.Get("http://127.0.0.1:11434/v1")
	if err != nil {
		t.Fatalf("loopback request: %v", err)
	}
	resp.Body.Close()
	if base.requests != 1 {
		t.Fatalf("requests = %d, want 1", base.requests)
	}
	if _, err := client.Get("https://api.openai.com/v1"); !errors.Is(err, egress.ErrBlocked) {
		t.Fatalf("blocked error = %v", err)
	}
	if base.requests != 1 {
		t.Fatalf("blocked request reached base transport")
	}
}

type countingTransport struct{ requests int }

func (t *countingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	t.requests++
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("{}")),
	}, nil
}
