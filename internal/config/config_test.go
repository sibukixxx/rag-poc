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
	t.Setenv("FORGEAI_PRIVACY_MODE", "local_only")

	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Port != 7000 {
		t.Errorf("expected env override port 7000, got %d", cfg.Server.Port)
	}
	if cfg.Privacy.Mode != "local_only" {
		t.Errorf("expected privacy mode env override, got %q", cfg.Privacy.Mode)
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

func TestLoadParsesRetentionAndStorageAtRest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "forgeai.yaml")
	yaml := "retention:\n  traces_days: 30\n  evaluation_runs_days: 90\nsecurity:\n  storage_at_rest: operator_encrypted_volume\n"
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(path)

	if err != nil {
		t.Fatal(err)
	}
	if cfg.Retention != (config.RetentionConfig{TraceDays: 30, EvaluationRunDays: 90}) {
		t.Fatalf("retention = %+v", cfg.Retention)
	}
	if cfg.Security.StorageAtRest != config.StorageAtRestOperatorEncryptedVolume {
		t.Fatalf("storage_at_rest = %q", cfg.Security.StorageAtRest)
	}
}

func TestLoadRejectsInvalidRetentionAndStorageAtRest(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{"negative trace retention", "retention:\n  traces_days: -1\n", "retention.traces_days must be >= 0 (0 keeps traces), got -1"},
		{"negative run retention", "retention:\n  evaluation_runs_days: -5\n", "retention.evaluation_runs_days must be >= 0 (0 keeps runs), got -5"},
		{"unknown storage declaration", "security:\n  storage_at_rest: encrypted\n", `security.storage_at_rest must be "" or "operator_encrypted_volume", got "encrypted"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "forgeai.yaml")
			if err := os.WriteFile(path, []byte(tt.yaml), 0o600); err != nil {
				t.Fatal(err)
			}

			_, err := config.Load(path)

			if err == nil || err.Error() != "invalid config "+path+": "+tt.want {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}
