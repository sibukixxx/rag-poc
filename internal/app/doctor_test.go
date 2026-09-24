package app

import (
	"reflect"
	"strings"
	"testing"

	"github.com/sibukixxx/rag-poc/internal/config"
	"github.com/sibukixxx/rag-poc/internal/domain/outbound"
)

func TestLLMProviderChecksHintDoesNotSuggestUnnamedSecretWhenAPIKeySecretUnset(t *testing.T) {
	t.Setenv("FORGEAI_OPENAI_API_KEY", "")
	cfg := config.Default()

	checks := llmProviderChecks(cfg, nil)

	if len(checks) == 0 {
		t.Fatal("expected alias checks for the default config")
	}
	for _, c := range checks {
		if c.OK {
			t.Fatalf("%s: expected failure without an API key", c.Name)
		}
		if strings.Contains(c.Info, "secret set `") {
			t.Fatalf("%s: hint suggests `forgeai secret set` without a secret name: %q", c.Name, c.Info)
		}
		if !strings.Contains(c.Info, "api_key_secret") {
			t.Fatalf("%s: hint should explain api_key_secret is required for stored secrets: %q", c.Name, c.Info)
		}
	}
}

func TestEmbeddingCheckHintNamesConfiguredSecret(t *testing.T) {
	t.Setenv("FORGEAI_OPENAI_API_KEY", "")
	cfg := config.Default()
	cfg.Embedding.Provider.APIKeySecret = "openai"

	got := embeddingCheck(cfg, nil)

	if got.OK {
		t.Fatal("expected failure without an API key")
	}
	if !strings.Contains(got.Info, "`forgeai secret set openai`") {
		t.Fatalf("hint should name the configured secret: %q", got.Info)
	}
}

func TestStorageAtRestCheckReportsUndeclaredStorageAsPlaintextRisk(t *testing.T) {
	got := storageAtRestCheck(config.Default())

	want := CheckStatus{
		Name: "Storage at rest", OK: true,
		Info: "not declared: chunk text is stored as plaintext in SQLite; put the database on an encrypted volume and set security.storage_at_rest: operator_encrypted_volume before ingesting confidential data",
	}
	if got != want {
		t.Fatalf("storageAtRestCheck = %+v, want %+v", got, want)
	}
}

func TestStorageAtRestCheckReportsOperatorDeclaration(t *testing.T) {
	cfg := config.Default()
	cfg.Security.StorageAtRest = config.StorageAtRestOperatorEncryptedVolume

	got := storageAtRestCheck(cfg)

	want := CheckStatus{Name: "Storage at rest", OK: true, Info: "operator declares an encrypted volume (not verified by ForgeAI)"}
	if got != want {
		t.Fatalf("storageAtRestCheck = %+v, want %+v", got, want)
	}
}

func TestRetentionCheckDescribesPolicy(t *testing.T) {
	cfg := config.Default()
	cfg.Retention = config.RetentionConfig{TraceDays: 30}

	got := retentionCheck(cfg)

	want := CheckStatus{Name: "Retention", OK: true, Info: "traces=30d evaluation_runs=keep audit=keep (apply with `forgeai data retention`)"}
	if got != want {
		t.Fatalf("retentionCheck = %+v, want %+v", got, want)
	}
}

func TestProductionProfileChecksFailOnSharedDemoAuthAndUndeclaredBoundaries(t *testing.T) {
	cfg := config.Default()
	cfg.Profile = config.ProfileProduction

	got := productionProfileChecks(cfg, true)

	want := []CheckStatus{
		{Name: "Production tenancy", OK: false, Info: "demo authentication is enabled: demo accounts share one ForgeAI workspace and are not tenant isolation; run one ForgeAI instance per customer and disable FORGEAI_DEMO_AUTH_ENABLED"},
		{Name: "Production management boundary", OK: false, Info: "not declared: put /api/v1 and the UI behind an authenticating reverse proxy and set security.management_boundary: reverse_proxy_identity"},
		{Name: "Production storage at rest", OK: false, Info: "not declared: set security.storage_at_rest: operator_encrypted_volume once the database is on an encrypted volume"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("productionProfileChecks:\nwant %+v\ngot  %+v", want, got)
	}
}

func TestProductionProfileChecksPassWhenSingleTenantAndDeclared(t *testing.T) {
	cfg := config.Default()
	cfg.Profile = config.ProfileProduction
	cfg.Security.ManagementBoundary = config.ManagementBoundaryReverseProxyIdentity
	cfg.Security.StorageAtRest = config.StorageAtRestOperatorEncryptedVolume

	got := productionProfileChecks(cfg, false)

	want := []CheckStatus{
		{Name: "Production tenancy", OK: true, Info: "single-tenant: one ForgeAI instance serves one customer security boundary"},
		{Name: "Production management boundary", OK: true, Info: "operator declares an authenticating reverse proxy (not verified by ForgeAI)"},
		{Name: "Production storage at rest", OK: true, Info: "operator declares an encrypted volume (not verified by ForgeAI)"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("productionProfileChecks:\nwant %+v\ngot  %+v", want, got)
	}
}

func TestOutboundPolicyCheckStatesScopeAndLimits(t *testing.T) {
	tests := []struct {
		name    string
		privacy config.PrivacyConfig
		want    string
	}{
		{"allow", config.PrivacyConfig{}, "policy=allow: retrieved text and questions are sent to providers unchanged"},
		{"redact with custom rule", config.PrivacyConfig{
			OutboundPolicy: "redact_known_patterns",
			SensitiveRules: []outbound.Rule{{Name: "employee_id", Pattern: `EMP-\d{6}`}},
		}, "policy=redact_known_patterns detectors=email,phone rules=employee_id (deterministic patterns only; other personal data is not detected)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Privacy = tt.privacy

			got := outboundPolicyCheck(cfg)

			if want := (CheckStatus{Name: "Outbound sensitive data", OK: true, Info: tt.want}); got != want {
				t.Fatalf("outboundPolicyCheck = %+v, want %+v", got, want)
			}
		})
	}
}
