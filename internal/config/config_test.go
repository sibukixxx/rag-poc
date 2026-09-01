package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sibukixxx/rag-poc/internal/config"
)

func TestLoadDefaultsWithoutFile(t *testing.T) {
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("expected default port 8080, got %d", cfg.Server.Port)
	}
	if cfg.Database.Type != "sqlite" {
		t.Errorf("expected default database type sqlite, got %s", cfg.Database.Type)
	}
}

func TestLoadMergesYAMLFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "forgeai.yaml")
	yaml := "server:\n  port: 9090\n"
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatalf("writing config file: %v", err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Port != 9090 {
		t.Errorf("expected port 9090 from file, got %d", cfg.Server.Port)
	}
	// Untouched fields keep their defaults.
	if cfg.Database.Type != "sqlite" {
		t.Errorf("expected default database type to survive merge, got %s", cfg.Database.Type)
	}
}

func TestEnvOverridesTakePrecedence(t *testing.T) {
	t.Setenv("FORGEAI_PORT", "7000")

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Port != 7000 {
		t.Errorf("expected env override port 7000, got %d", cfg.Server.Port)
	}
}

func TestEnsureDirsCreatesDataDirectories(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Database.Path = filepath.Join(dir, "nested", "forgeai.db")
	cfg.Storage.Path = filepath.Join(dir, "files")

	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(cfg.Database.Path)); err != nil {
		t.Errorf("expected database dir to exist: %v", err)
	}
	if _, err := os.Stat(cfg.Storage.Path); err != nil {
		t.Errorf("expected storage dir to exist: %v", err)
	}
}
