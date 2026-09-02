package app

import (
	"database/sql"
	"fmt"

	"github.com/sibukixxx/rag-poc/internal/adapter/crypto"
	"github.com/sibukixxx/rag-poc/internal/adapter/sqlite"
	"github.com/sibukixxx/rag-poc/internal/config"
	"github.com/sibukixxx/rag-poc/internal/domain/secret"
)

// Version is set at build time via -ldflags; "dev" is used for local builds.
var Version = "dev"

// App holds the wired-up dependencies a running ForgeAI server needs.
type App struct {
	Config config.Config
	DB     *sql.DB
}

// Bootstrap loads configuration, ensures data directories exist, and opens
// the database (running migrations). Callers must Close() the returned App.
func Bootstrap(configPath string) (*App, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}

	if err := cfg.EnsureDirs(); err != nil {
		return nil, err
	}

	if cfg.Database.Type != "sqlite" {
		return nil, fmt.Errorf("unsupported database type %q (v0.1 supports sqlite only)", cfg.Database.Type)
	}

	db, err := sqlite.Open(cfg.Database.Path)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	return &App{Config: cfg, DB: db}, nil
}

// Secrets builds a SecretStore backed by the master key. It returns an
// error if the master key environment variable isn't set — callers that
// can tolerate a missing key (e.g. BuildRouter, which falls back to
// APIKeyEnv) should treat that as "no secret store available", not a
// fatal condition.
func (a *App) Secrets() (secret.Store, error) {
	box, err := crypto.NewSecretBox(a.Config.Security.EncryptionKeyEnv)
	if err != nil {
		return nil, err
	}
	return sqlite.NewSecretStore(a.DB, box), nil
}

func (a *App) Close() error {
	if a.DB != nil {
		return a.DB.Close()
	}
	return nil
}
