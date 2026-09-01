package app

import (
	"fmt"
	"os"

	"github.com/sibukixxx/rag-poc/internal/adapter/sqlite"
	"github.com/sibukixxx/rag-poc/internal/config"
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

	if cfg.Database.Type == "sqlite" {
		if db, err := sqlite.Open(cfg.Database.Path); err != nil {
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

	if key := os.Getenv(cfg.Security.EncryptionKeyEnv); key == "" {
		checks = append(checks, CheckStatus{
			Name: "Master key",
			OK:   false,
			Info: fmt.Sprintf("%s not set; run `forgeai init` or set it before storing secrets", cfg.Security.EncryptionKeyEnv),
		})
	} else {
		checks = append(checks, CheckStatus{Name: "Master key", OK: true, Info: cfg.Security.EncryptionKeyEnv + " is set"})
	}

	return checks
}
