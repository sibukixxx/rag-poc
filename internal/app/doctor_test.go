package app

import (
	"strings"
	"testing"

	"github.com/sibukixxx/rag-poc/internal/config"
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

	want := CheckStatus{Name: "Retention", OK: true, Info: "traces=30d evaluation_runs=keep (apply with `forgeai data retention`)"}
	if got != want {
		t.Fatalf("retentionCheck = %+v, want %+v", got, want)
	}
}
