package app

import (
	"context"
	"reflect"
	"testing"

	"github.com/sibukixxx/rag-poc/internal/config"
	"github.com/sibukixxx/rag-poc/internal/domain/audit"
)

type memoryAudit struct{ events []audit.Event }

func (m *memoryAudit) Record(_ context.Context, e audit.Event) error {
	m.events = append(m.events, e)
	return nil
}

func TestRecordStartupAuditsProfileAndEgressDestinationsWithoutKeys(t *testing.T) {
	cfg := config.Default()
	cfg.Profile = config.ProfileProduction
	cfg.Privacy = config.PrivacyConfig{Mode: "local_only", OutboundPolicy: "deny_sensitive", AllowedDestinations: []string{"https://llm.internal:8443"}}
	cfg.LLM.Providers["default"] = config.ProviderConfig{Type: "openai_compatible", BaseURL: "https://user:pw@llm.internal:8443/v1?x=1", APIKeyEnv: "SECRET_ENV"}
	cfg.Embedding.Provider.BaseURL = "http://127.0.0.1:11434/v1"
	rec := &memoryAudit{}

	recordStartup(context.Background(), rec, cfg, "v-test")

	if len(rec.events) != 1 {
		t.Fatalf("events = %+v", rec.events)
	}
	want := map[string]string{
		"version": "v-test", "profile": "production", "privacy_mode": "local_only", "outbound_policy": "deny_sensitive",
		"allowed_destinations": "https://llm.internal:8443",
		"llm_endpoints":        "default=https://llm.internal:8443",
		"embedding_endpoint":   "http://127.0.0.1:11434",
	}
	e := rec.events[0]
	if e.Action != audit.ActionServerStart || e.Actor != "system" || !reflect.DeepEqual(e.Metadata, want) {
		t.Fatalf("event = %+v, want server.start with metadata %v", e, want)
	}
}
