package app

import (
	"context"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/sibukixxx/rag-poc/internal/adapter/sqlite"
	"github.com/sibukixxx/rag-poc/internal/config"
	"github.com/sibukixxx/rag-poc/internal/domain/audit"
)

// Audit returns the security audit trail store for this App's database.
func (a *App) Audit() *sqlite.AuditStore {
	return sqlite.NewAuditStore(a.DB)
}

// recordStartup records which profile and provider destinations a server
// process runs with, so a change of egress configuration between restarts
// is visible in the audit trail. Only origins are recorded: URL paths,
// query strings, userinfo, and API keys never are.
func recordStartup(ctx context.Context, rec audit.Recorder, cfg config.Config, version string) {
	profile := cfg.Profile
	if profile == "" {
		profile = config.ProfileDevelopment
	}
	mode := cfg.Privacy.Mode
	if mode == "" {
		mode = "external_allowed"
	}
	outboundPolicy := cfg.Privacy.OutboundPolicy
	if outboundPolicy == "" {
		outboundPolicy = "allow"
	}
	names := make([]string, 0, len(cfg.LLM.Providers))
	for name := range cfg.LLM.Providers {
		names = append(names, name)
	}
	sort.Strings(names)
	llmEndpoints := make([]string, 0, len(names))
	for _, name := range names {
		llmEndpoints = append(llmEndpoints, name+"="+urlOrigin(cfg.LLM.Providers[name].BaseURL))
	}
	allowed := make([]string, 0, len(cfg.Privacy.AllowedDestinations))
	for _, d := range cfg.Privacy.AllowedDestinations {
		allowed = append(allowed, urlOrigin(d))
	}
	_ = rec.Record(ctx, audit.Event{
		OccurredAt: time.Now(), Action: audit.ActionServerStart, Outcome: audit.OutcomeSuccess,
		Actor: audit.ActorFrom(ctx),
		Metadata: map[string]string{
			"version": version, "profile": profile, "privacy_mode": mode, "outbound_policy": outboundPolicy,
			"allowed_destinations": strings.Join(allowed, ","),
			"llm_endpoints":        strings.Join(llmEndpoints, ","),
			"embedding_endpoint":   urlOrigin(cfg.Embedding.Provider.BaseURL),
		},
	})
}

func urlOrigin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "invalid"
	}
	return u.Scheme + "://" + u.Host
}
