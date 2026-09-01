package app

import (
	"database/sql"
	"fmt"
	"os"
	"sort"

	"github.com/sibukixxx/rag-poc/internal/adapter/crypto"
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

	return checks
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
				Info: fmt.Sprintf("%s -> %s: no API key (set %s or `forgeai secret set %s`)", target.Provider, target.Model, provider.APIKeyEnv, provider.APIKeySecret),
			})
		}
	}

	return checks
}
