package app

import (
	"database/sql"
	"fmt"

	"github.com/sibukixxx/rag-poc/internal/adapter/sqlite"
	"github.com/sibukixxx/rag-poc/internal/config"
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

func (a *App) Close() error {
	if a.DB != nil {
		return a.DB.Close()
	}
	return nil
}
