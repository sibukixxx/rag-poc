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
