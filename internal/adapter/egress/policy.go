// Package egress enforces the configured provider-network boundary at the
// HTTP transport layer. Keeping the check here prevents chat, embeddings,
// judges, rerankers, and future use cases from bypassing it accidentally.
package egress

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

const (
	ModeExternalAllowed = "external_allowed"
	ModeLocalOnly       = "local_only"
)

var ErrBlocked = errors.New("provider destination blocked by egress policy")

type Policy struct {
	Mode                string
	AllowedDestinations []string
	AllowPrivateNetwork bool
}

func (p Policy) normalizedMode() string {
	mode := strings.ToLower(strings.TrimSpace(p.Mode))
	if mode == "" {
		return ModeExternalAllowed
	}
	return mode
}

func (p Policy) Validate() error {
	switch p.normalizedMode() {
	case ModeExternalAllowed, ModeLocalOnly:
	default:
		return fmt.Errorf("unknown privacy mode %q", p.Mode)
	}
	for _, raw := range p.AllowedDestinations {
		if _, err := canonicalOrigin(raw); err != nil {
			return fmt.Errorf("invalid allowed destination %q: %w", raw, err)
		}
	}
	return nil
}

func (p Policy) Check(rawURL string) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if p.normalizedMode() == ModeExternalAllowed {
		return nil
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return fmt.Errorf("%w: invalid URL", ErrBlocked)
	}
	origin, err := canonicalOrigin(rawURL)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBlocked, err)
	}
	for _, allowed := range p.AllowedDestinations {
		a, _ := canonicalOrigin(allowed) // Validate already checked it.
		if origin == a {
			return nil
		}
	}

	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || (p.AllowPrivateNetwork && ip.IsPrivate()) {
			return nil
		}
	}
	return fmt.Errorf("%w: %s is not local or explicitly approved", ErrBlocked, origin)
}

func canonicalOrigin(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" {
		return "", errors.New("must be an absolute URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("scheme must be http or https")
	}
	return strings.ToLower(u.Scheme + "://" + u.Host), nil
}

// Transport performs the final fail-closed check immediately before bytes
// can leave the process.
type Transport struct {
	Policy Policy
	Base   http.RoundTripper
}

func (t Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := t.Policy.Check(req.URL.String()); err != nil {
		return nil, err
	}
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}
