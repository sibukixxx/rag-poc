package app

import (
	"context"
	"os"

	"github.com/sibukixxx/rag-poc/internal/adapter/openaicompat"
	"github.com/sibukixxx/rag-poc/internal/config"
	"github.com/sibukixxx/rag-poc/internal/domain/llm"
	"github.com/sibukixxx/rag-poc/internal/domain/secret"
)

// BuildRouter wires the LLM Router and PriceTable from config. It never
// fails on a missing API key — a provider with no resolvable key is still
// registered, so `forgeai serve` always starts; the missing key surfaces
// as an API error on first use, and as a FAIL row in `forgeai doctor`.
func BuildRouter(cfg config.LLMConfig, secrets secret.Store) *llm.Router {
	router := llm.NewRouter()

	for name, p := range cfg.Providers {
		switch p.Type {
		case "openai_compatible", "":
			apiKey := resolveAPIKey(p, secrets)
			router.RegisterProvider(name, openaicompat.New(p.BaseURL, apiKey))
		}
	}

	for alias, target := range cfg.Aliases {
		router.RegisterAlias(alias, llm.Alias{Provider: target.Provider, Model: target.Model})
	}

	return router
}

// resolveAPIKey prefers an environment variable (the local-dev fast path)
// and falls back to the encrypted secret store. It returns "" rather than
// an error when neither yields a key, per BuildRouter's resilience contract.
func resolveAPIKey(p config.ProviderConfig, secrets secret.Store) string {
	if p.APIKeyEnv != "" {
		if v := os.Getenv(p.APIKeyEnv); v != "" {
			return v
		}
	}
	if p.APIKeySecret != "" && secrets != nil {
		if v, err := secrets.Get(context.Background(), p.APIKeySecret); err == nil {
			return string(v)
		}
	}
	return ""
}

// HasAPIKey reports whether a provider's key currently resolves, for use
// by `forgeai doctor` (it re-derives the same precedence as BuildRouter).
func HasAPIKey(p config.ProviderConfig, secrets secret.Store) bool {
	return resolveAPIKey(p, secrets) != ""
}

func BuildPriceTable(cfg config.LLMConfig) llm.PriceTable {
	prices := make(map[string]llm.ModelPricing, len(cfg.Pricing))
	for model, p := range cfg.Pricing {
		prices[model] = llm.ModelPricing{InputPer1M: p.InputPer1M, OutputPer1M: p.OutputPer1M}
	}
	rate := cfg.Currency.USDRate
	if rate == 0 {
		rate = 1
	}
	currency := cfg.Currency.Display
	if currency == "" {
		currency = "USD"
	}
	return llm.PriceTable{
		Prices:          prices,
		DisplayCurrency: currency,
		USDToDisplay:    rate,
	}
}
