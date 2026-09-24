package config_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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
		{"unknown outbound policy", "privacy:\n  outbound_policy: block\n", `privacy.outbound_policy: outbound policy must be one of allow, deny_sensitive, redact_known_patterns; got "block"`},
		{"malformed sensitive rule", "privacy:\n  sensitive_rules:\n    - name: acct\n      pattern: 'ACCT-(\\d+'\n", "privacy: sensitive rule \"acct\": invalid pattern: error parsing regexp: missing closing ): `ACCT-(\\d+`"},
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

func TestLoadParsesFilesystemSourceSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "forgeai.yaml")
	yaml := "sources:\n  filesystem:\n    allowed_roots: [/srv/share, /mnt/nas]\n    workers: 8\n    max_file_bytes: 1048576\n"
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(path)

	if err != nil {
		t.Fatal(err)
	}
	want := config.FilesystemSourceConfig{AllowedRoots: []string{"/srv/share", "/mnt/nas"}, Workers: 8, MaxFileBytes: 1048576}
	if !reflect.DeepEqual(cfg.Sources.Filesystem, want) {
		t.Fatalf("filesystem sources = %+v, want %+v", cfg.Sources.Filesystem, want)
	}
}

func TestLoadRejectsInvalidFilesystemSourceSettings(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{"relative root", "sources:\n  filesystem:\n    allowed_roots: [share]\n", `sources.filesystem.allowed_roots: "share" must be an absolute path`},
		{"too many workers", "sources:\n  filesystem:\n    workers: 100\n", "sources.filesystem.workers must be between 0 and 64, got 100"},
		{"negative size", "sources:\n  filesystem:\n    max_file_bytes: -1\n", "sources.filesystem.max_file_bytes must be >= 0, got -1"},
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

func TestLoadParsesOAuthProvidersWithPKCEOnByDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "forgeai.yaml")
	yaml := `sources:
  oauth_providers:
    corp_idp:
      authorization_url: https://idp.example/authorize
      token_url: https://idp.example/token
      client_id: forgeai
      client_secret_secret: corp-idp-client
      scopes: [files.read]
      redirect_url: https://forgeai.corp.example/oauth/callback
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(path)

	if err != nil {
		t.Fatal(err)
	}
	p := cfg.Sources.OAuthProviders["corp_idp"]
	if p.ClientID != "forgeai" || p.ClientSecretSecret != "corp-idp-client" || !p.PKCEEnabled() || p.Scopes[0] != "files.read" {
		t.Fatalf("provider = %+v", p)
	}
}

func TestLoadRejectsUnsafeOAuthProviderSettings(t *testing.T) {
	base := "sources:\n  oauth_providers:\n    idp:\n      authorization_url: %s\n      token_url: https://idp.example/token\n      client_id: c\n      redirect_url: %s\n"
	tests := []struct {
		name, authURL, redirect, want string
	}{
		{"plain http provider", "http://idp.example/authorize", "https://f.example/oauth/callback", `sources.oauth_providers.idp.authorization_url must be an https URL (http is allowed only for localhost), got "http://idp.example/authorize"`},
		{"wrong callback path", "https://idp.example/authorize", "https://f.example/callback", `sources.oauth_providers.idp.redirect_url must end with /oauth/callback, got "https://f.example/callback"`},
		{"local dev redirect is fine but relative is not", "https://idp.example/authorize", "/oauth/callback", `sources.oauth_providers.idp.redirect_url must be an https URL (http is allowed only for localhost), got "/oauth/callback"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "forgeai.yaml")
			if err := os.WriteFile(path, []byte(fmt.Sprintf(base, tt.authURL, tt.redirect)), 0o600); err != nil {
				t.Fatal(err)
			}

			_, err := config.Load(path)

			if err == nil || err.Error() != "invalid config "+path+": "+tt.want {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}
