package app

import (
	"database/sql"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/sibukixxx/rag-poc/internal/adapter/crypto"
	"github.com/sibukixxx/rag-poc/internal/adapter/egress"
	"github.com/sibukixxx/rag-poc/internal/adapter/sqlite"
	"github.com/sibukixxx/rag-poc/internal/config"
	"github.com/sibukixxx/rag-poc/internal/domain/secret"
)

// CheckStatus is one row of `forgeai doctor` output.
type CheckStatus struct {
	Name string
	OK   bool
	Info string
}

// Doctor runs environment/config sanity checks without requiring a fully
// bootstrapped App, so it still reports useful info when setup is broken.
func Doctor(configPath string) []CheckStatus {
	var checks []CheckStatus

	cfg, err := config.Load(configPath)
	if err != nil {
		checks = append(checks, CheckStatus{Name: "Config", OK: false, Info: err.Error()})
		return checks
	}
	checks = append(checks, CheckStatus{Name: "Config", OK: true, Info: "loaded"})
	checks = append(checks, privacyCheck(cfg))
	checks = append(checks, outboundPolicyCheck(cfg))
	checks = append(checks, storageAtRestCheck(cfg), retentionCheck(cfg))

	if err := cfg.EnsureDirs(); err != nil {
		checks = append(checks, CheckStatus{Name: "Filesystem", OK: false, Info: err.Error()})
	} else {
		checks = append(checks, CheckStatus{Name: "Filesystem", OK: true, Info: cfg.Storage.Path})
	}

	var db *sql.DB
	if cfg.Database.Type == "sqlite" {
		var err error
		db, err = sqlite.Open(cfg.Database.Path)
		if err != nil {
			checks = append(checks, CheckStatus{Name: "Database", OK: false, Info: err.Error()})
		} else {
			defer db.Close()
			if err := db.Ping(); err != nil {
				checks = append(checks, CheckStatus{Name: "Database", OK: false, Info: err.Error()})
			} else {
				checks = append(checks, CheckStatus{Name: "Database", OK: true, Info: cfg.Database.Path + " (migrations applied)"})
			}
		}
	}

	var secrets secret.Store
	if key := os.Getenv(cfg.Security.EncryptionKeyEnv); key == "" {
		checks = append(checks, CheckStatus{
			Name: "Master key",
			OK:   false,
			Info: fmt.Sprintf("%s not set; run `forgeai init` or set it before storing secrets", cfg.Security.EncryptionKeyEnv),
		})
	} else {
		checks = append(checks, CheckStatus{Name: "Master key", OK: true, Info: cfg.Security.EncryptionKeyEnv + " is set"})
		if db != nil {
			if box, err := crypto.NewSecretBox(cfg.Security.EncryptionKeyEnv); err == nil {
				secrets = sqlite.NewSecretStore(db, box)
			}
		}
	}

	checks = append(checks, llmProviderChecks(cfg, secrets)...)
	checks = append(checks, embeddingCheck(cfg, secrets))
	if cfg.Profile == config.ProfileProduction {
		checks = append(checks, productionProfileChecks(cfg, envBool("FORGEAI_DEMO_AUTH_ENABLED"))...)
	}

	return checks
}

func privacyCheck(cfg config.Config) CheckStatus {
	p := buildEgressPolicy(cfg.Privacy)
	if err := p.Validate(); err != nil {
		return CheckStatus{Name: "Privacy / egress", OK: false, Info: err.Error()}
	}
	var blocked []string
	for _, provider := range cfg.LLM.Providers {
		if err := p.Check(provider.BaseURL); err != nil {
			blocked = append(blocked, provider.BaseURL)
		}
	}
	if err := p.Check(cfg.Embedding.Provider.BaseURL); err != nil {
		blocked = append(blocked, cfg.Embedding.Provider.BaseURL)
	}
	if len(blocked) > 0 {
		return CheckStatus{Name: "Privacy / egress", OK: false, Info: fmt.Sprintf("mode=%s; blocked provider destinations: %v", cfg.Privacy.Mode, blocked)}
	}
	mode := cfg.Privacy.Mode
	if mode == "" {
		mode = egress.ModeExternalAllowed
	}
	return CheckStatus{Name: "Privacy / egress", OK: true, Info: fmt.Sprintf("mode=%s; LLM=%v; embedding=%s", mode, providerURLs(cfg), cfg.Embedding.Provider.BaseURL)}
}

func providerURLs(cfg config.Config) []string {
	urls := make([]string, 0, len(cfg.LLM.Providers))
	for _, provider := range cfg.LLM.Providers {
		urls = append(urls, provider.BaseURL)
	}
	sort.Strings(urls)
	return urls
}

func embeddingCheck(cfg config.Config, secrets secret.Store) CheckStatus {
	if HasAPIKey(cfg.Embedding.Provider, secrets) {
		return CheckStatus{
			Name: "Embedding model", OK: true,
			Info: fmt.Sprintf("%s (%d dims, key resolved)", cfg.Embedding.Model, cfg.Embedding.Dimensions),
		}
	}
	return CheckStatus{
		Name: "Embedding model", OK: false,
		Info: fmt.Sprintf("%s: %s", cfg.Embedding.Model, missingKeyHint(cfg.Embedding.Provider)),
	}
}

// llmProviderChecks reports, per configured alias, whether it resolves to
// a registered provider and whether that provider's API key is currently
// resolvable — without making a network call, so `doctor` stays instant
// and works offline.
func llmProviderChecks(cfg config.Config, secrets secret.Store) []CheckStatus {
	var checks []CheckStatus

	aliases := make([]string, 0, len(cfg.LLM.Aliases))
	for alias := range cfg.LLM.Aliases {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)

	for _, alias := range aliases {
		target := cfg.LLM.Aliases[alias]
		provider, ok := cfg.LLM.Providers[target.Provider]
		name := fmt.Sprintf("LLM alias %q", alias)
		if !ok {
			checks = append(checks, CheckStatus{Name: name, OK: false, Info: fmt.Sprintf("references unknown provider %q", target.Provider)})
			continue
		}
		if HasAPIKey(provider, secrets) {
			checks = append(checks, CheckStatus{Name: name, OK: true, Info: fmt.Sprintf("%s -> %s (key resolved)", target.Provider, target.Model)})
		} else {
			checks = append(checks, CheckStatus{
				Name: name, OK: false,
				Info: fmt.Sprintf("%s -> %s: %s", target.Provider, target.Model, missingKeyHint(provider)),
			})
		}
	}

	return checks
}

// missingKeyHint tells the operator how to supply a provider key. A stored
// secret is only consulted when the provider names it via api_key_secret,
// so suggesting `forgeai secret set` without that name would be a dead end.
func missingKeyHint(p config.ProviderConfig) string {
	var ways []string
	if p.APIKeyEnv != "" {
		ways = append(ways, "export "+p.APIKeyEnv)
	}
	if p.APIKeySecret != "" {
		ways = append(ways, fmt.Sprintf("`forgeai secret set %s`", p.APIKeySecret))
	} else {
		ways = append(ways, "set api_key_secret: <name> in the provider config and run `forgeai secret set <name>`")
	}
	return "no API key (" + strings.Join(ways, " or ") + ")"
}

func storageAtRestCheck(cfg config.Config) CheckStatus {
	name := "Storage at rest"
	if cfg.Security.StorageAtRest == config.StorageAtRestOperatorEncryptedVolume {
		return CheckStatus{Name: name, OK: true, Info: "operator declares an encrypted volume (not verified by ForgeAI)"}
	}
	return CheckStatus{
		Name: name, OK: true,
		Info: "not declared: chunk text is stored as plaintext in SQLite; put the database on an encrypted volume and set security.storage_at_rest: operator_encrypted_volume before ingesting confidential data",
	}
}

func retentionCheck(cfg config.Config) CheckStatus {
	days := func(n int) string {
		if n == 0 {
			return "keep"
		}
		return fmt.Sprintf("%dd", n)
	}
	return CheckStatus{
		Name: "Retention", OK: true,
		Info: fmt.Sprintf("traces=%s evaluation_runs=%s audit=%s (apply with `forgeai data retention`)", days(cfg.Retention.TraceDays), days(cfg.Retention.EvaluationRunDays), days(cfg.Retention.AuditDays)),
	}
}

// productionProfileChecks fail on settings that are fine for demos but not
// for customer data. ForgeAI v0.1 is single-tenant: one instance per
// customer security boundary (#24). Declarations are operator statements;
// ForgeAI cannot verify a reverse proxy or an encrypted disk.
func productionProfileChecks(cfg config.Config, demoAuthEnabled bool) []CheckStatus {
	tenancy := CheckStatus{Name: "Production tenancy", OK: true, Info: "single-tenant: one ForgeAI instance serves one customer security boundary"}
	if demoAuthEnabled {
		tenancy = CheckStatus{Name: "Production tenancy", OK: false, Info: "demo authentication is enabled: demo accounts share one ForgeAI workspace and are not tenant isolation; run one ForgeAI instance per customer and disable FORGEAI_DEMO_AUTH_ENABLED"}
	}
	boundary := CheckStatus{Name: "Production management boundary", OK: true, Info: "operator declares an authenticating reverse proxy (not verified by ForgeAI)"}
	if cfg.Security.ManagementBoundary != config.ManagementBoundaryReverseProxyIdentity {
		boundary = CheckStatus{Name: "Production management boundary", OK: false, Info: "not declared: put /api/v1 and the UI behind an authenticating reverse proxy and set security.management_boundary: reverse_proxy_identity"}
	}
	storage := CheckStatus{Name: "Production storage at rest", OK: true, Info: "operator declares an encrypted volume (not verified by ForgeAI)"}
	if cfg.Security.StorageAtRest != config.StorageAtRestOperatorEncryptedVolume {
		storage = CheckStatus{Name: "Production storage at rest", OK: false, Info: "not declared: set security.storage_at_rest: operator_encrypted_volume once the database is on an encrypted volume"}
	}
	return []CheckStatus{tenancy, boundary, storage}
}

// outboundPolicyCheck states which sensitive-data policy applies to text
// sent to providers and what its detection does not cover (#25).
func outboundPolicyCheck(cfg config.Config) CheckStatus {
	name := "Outbound sensitive data"
	policy := cfg.Privacy.OutboundPolicy
	if policy == "" || policy == "allow" {
		return CheckStatus{Name: name, OK: true, Info: "policy=allow: retrieved text and questions are sent to providers unchanged"}
	}
	rules := make([]string, len(cfg.Privacy.SensitiveRules))
	for i, r := range cfg.Privacy.SensitiveRules {
		rules[i] = r.Name
	}
	info := fmt.Sprintf("policy=%s detectors=%s", policy, strings.Join(cfg.Privacy.Detectors(), ","))
	if len(rules) > 0 {
		info += " rules=" + strings.Join(rules, ",")
	}
	return CheckStatus{Name: name, OK: true, Info: info + " (deterministic patterns only; other personal data is not detected)"}
}
