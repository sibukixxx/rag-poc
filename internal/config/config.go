// Package config loads ForgeAI's server configuration from a YAML file,
// then applies FORGEAI_* environment variable overrides on top.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"gopkg.in/yaml.v3"

	"github.com/sibukixxx/rag-poc/internal/domain/outbound"
)

type Config struct {
	// Profile is "development" (default) or "production". The production
	// profile makes `forgeai doctor` fail on settings that are acceptable
	// for demos but unsafe for customer data (#24).
	Profile   string          `yaml:"profile"`
	Server    ServerConfig    `yaml:"server"`
	Database  DatabaseConfig  `yaml:"database"`
	Storage   StorageConfig   `yaml:"storage"`
	Security  SecurityConfig  `yaml:"security"`
	Privacy   PrivacyConfig   `yaml:"privacy"`
	LLM       LLMConfig       `yaml:"llm"`
	Embedding EmbeddingConfig `yaml:"embedding"`
	Retention RetentionConfig `yaml:"retention"`
	Sources   SourcesConfig   `yaml:"sources"`
}

// SourcesConfig configures source connectors that the server runs.
type SourcesConfig struct {
	Filesystem FilesystemSourceConfig `yaml:"filesystem"`
}

// FilesystemSourceConfig bounds local/NAS bulk ingestion (#30).
type FilesystemSourceConfig struct {
	// AllowedRoots are the only host directories that may be registered as
	// sources. Empty disables filesystem sources, so the management API can
	// never be used to read arbitrary server files.
	AllowedRoots []string `yaml:"allowed_roots"`
	// Workers is the number of files processed concurrently (0 = default 4).
	Workers int `yaml:"workers"`
	// MaxFileBytes skips larger files (0 = default 32 MiB).
	MaxFileBytes int64 `yaml:"max_file_bytes"`
}

type ServerConfig struct {
	Port int `yaml:"port"`
}

type DatabaseConfig struct {
	Type string `yaml:"type"` // "sqlite" in v0.1; "postgres" from v0.2
	Path string `yaml:"path"`
}

type StorageConfig struct {
	Type string `yaml:"type"` // "filesystem" in v0.1
	Path string `yaml:"path"`
}

type SecurityConfig struct {
	// EncryptionKeyEnv names the environment variable holding the base64
	// master key used to encrypt secrets at rest (see internal/adapter/crypto).
	EncryptionKeyEnv string `yaml:"encryption_key_env"`
	// StorageAtRest records what the operator guarantees about the disk that
	// holds the database. ForgeAI stores searchable chunk text in plaintext
	// (FTS needs it), so confidential corpora require an encrypted volume
	// provided by the infrastructure. "" means nothing has been declared.
	StorageAtRest string `yaml:"storage_at_rest"`
	// ManagementBoundary records how the management UI/API (/api/v1) is
	// protected. ForgeAI's own demo accounts share one workspace and are not
	// a production identity boundary, so production deployments put the
	// management surface behind an authenticating reverse proxy (Cloudflare
	// Access, OAuth2 Proxy, an SSO gateway...). "" means not declared.
	ManagementBoundary string `yaml:"management_boundary"`
}

const (
	ProfileDevelopment = "development"
	ProfileProduction  = "production"

	// ManagementBoundaryReverseProxyIdentity declares that /api/v1 and the UI
	// are only reachable through an authenticating reverse proxy.
	ManagementBoundaryReverseProxyIdentity = "reverse_proxy_identity"
)

// StorageAtRestOperatorEncryptedVolume declares that the database lives on
// a volume encrypted by the host/cloud (FileVault, LUKS, EBS encryption...).
const StorageAtRestOperatorEncryptedVolume = "operator_encrypted_volume"

// RetentionConfig bounds how long operational artifacts that can quote
// customer text are kept. 0 keeps them until explicitly deleted.
type RetentionConfig struct {
	TraceDays         int `yaml:"traces_days"`
	EvaluationRunDays int `yaml:"evaluation_runs_days"`
	// AuditDays is separate so security records can outlive ordinary traces.
	AuditDays int `yaml:"audit_days"`
}

// PrivacyConfig controls outbound provider traffic. external_allowed preserves
// the normal OpenAI-compatible behaviour. local_only fails closed unless the
// destination is loopback/private or its URL origin is explicitly allowlisted.
type PrivacyConfig struct {
	Mode                string   `yaml:"mode"`
	AllowedDestinations []string `yaml:"allowed_destinations"`
	AllowPrivateNetwork bool     `yaml:"allow_private_network"`
	// OutboundPolicy is the sensitive-data policy for text sent to model
	// providers (#25): allow (default), deny_sensitive, redact_known_patterns.
	OutboundPolicy string `yaml:"outbound_policy"`
	// SensitiveDetectors selects builtin detectors; nil means email+phone.
	SensitiveDetectors []string `yaml:"sensitive_detectors"`
	// SensitiveRules adds operator-defined RE2 patterns.
	SensitiveRules []outbound.Rule `yaml:"sensitive_rules"`
}

// DefaultSensitiveDetectors apply when sensitive_detectors is omitted.
var DefaultSensitiveDetectors = []string{"email", "phone"}

// Detectors returns the configured builtin detectors or the defaults.
func (p PrivacyConfig) Detectors() []string {
	if p.SensitiveDetectors == nil {
		return DefaultSensitiveDetectors
	}
	return p.SensitiveDetectors
}

// LLMConfig configures the LLM Router: named providers, business-facing
// aliases (cheap/normal/judge) that resolve to a provider+model, and the
// price table used to compute cost locally since providers don't return
// it (docs/V0.1_SPEC.md §4, docs/DESIGN_REVIEW.md F-8).
type LLMConfig struct {
	Providers map[string]ProviderConfig `yaml:"providers"`
	Aliases   map[string]AliasConfig    `yaml:"aliases"`
	Pricing   map[string]PricingConfig  `yaml:"pricing"`
	Currency  CurrencyConfig            `yaml:"currency"`
}

type ProviderConfig struct {
	Type string `yaml:"type"` // "openai_compatible" in v0.1
	// BaseURL is the API root, e.g. "https://api.openai.com/v1" — no
	// trailing slash, no "/chat/completions" suffix.
	BaseURL string `yaml:"base_url"`
	// APIKeyEnv, if set and present in the environment, is used directly
	// (the local-dev fast path). APIKeySecret, if set, is looked up in the
	// encrypted secret store instead (see `forgeai secret set`). If both
	// are set, APIKeyEnv wins when present.
	APIKeyEnv    string `yaml:"api_key_env"`
	APIKeySecret string `yaml:"api_key_secret"`
}

type AliasConfig struct {
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
}

type PricingConfig struct {
	InputPer1M  float64 `yaml:"input_per_1m"`
	OutputPer1M float64 `yaml:"output_per_1m"`
}

type CurrencyConfig struct {
	Display string  `yaml:"display"`  // e.g. "USD", "JPY"
	USDRate float64 `yaml:"usd_rate"` // multiplier from USD to Display
}

// EmbeddingConfig configures the single embedding model used to vectorize
// ingested chunks (docs/V0.1_SPEC.md §3). Unlike LLM, there's no
// alias/router layer — swapping embedding models requires re-ingesting,
// so v0.1 keeps it to one configured model.
type EmbeddingConfig struct {
	Provider   ProviderConfig `yaml:"provider"`
	Model      string         `yaml:"model"`
	Dimensions int            `yaml:"dimensions"`
}

// Default returns the configuration ForgeAI uses when no config file is
// present, so `forgeai serve` works with zero setup.
func Default() Config {
	return Config{
		Server: ServerConfig{Port: 8080},
		Database: DatabaseConfig{
			Type: "sqlite",
			Path: "./data/forgeai.db",
		},
		Storage: StorageConfig{
			Type: "filesystem",
			Path: "./data/files",
		},
		Security: SecurityConfig{
			EncryptionKeyEnv: "FORGEAI_MASTER_KEY",
		},
		Privacy: PrivacyConfig{
			Mode:                "external_allowed",
			AllowPrivateNetwork: true,
		},
		LLM: LLMConfig{
			Providers: map[string]ProviderConfig{
				"default": {
					Type:      "openai_compatible",
					BaseURL:   "https://api.openai.com/v1",
					APIKeyEnv: "FORGEAI_OPENAI_API_KEY",
				},
			},
			Aliases: map[string]AliasConfig{
				"cheap":  {Provider: "default", Model: "gpt-4o-mini"},
				"normal": {Provider: "default", Model: "gpt-4o-mini"},
				"judge":  {Provider: "default", Model: "gpt-4o-mini"},
			},
			Pricing: map[string]PricingConfig{
				"gpt-4o-mini":            {InputPer1M: 0.15, OutputPer1M: 0.60},
				"text-embedding-3-small": {InputPer1M: 0.02, OutputPer1M: 0},
			},
			Currency: CurrencyConfig{
				Display: "USD",
				USDRate: 1.0,
			},
		},
		Embedding: EmbeddingConfig{
			Provider: ProviderConfig{
				Type:      "openai_compatible",
				BaseURL:   "https://api.openai.com/v1",
				APIKeyEnv: "FORGEAI_OPENAI_API_KEY",
			},
			Model:      "text-embedding-3-small",
			Dimensions: 1536,
		},
	}
}

// Load reads a YAML config from path (if it exists), merges it onto the
// defaults, then applies environment variable overrides. path may be empty,
// in which case only defaults and env overrides apply.
func Load(path string) (Config, error) {
	cfg := Default()

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			if !os.IsNotExist(err) {
				return Config{}, fmt.Errorf("reading config %s: %w", path, err)
			}
		} else if err := yaml.Unmarshal(data, &cfg); err != nil {
			return Config{}, fmt.Errorf("parsing config %s: %w", path, err)
		}
	}

	applyEnvOverrides(&cfg)

	if err := cfg.validate(); err != nil {
		return Config{}, fmt.Errorf("invalid config %s: %w", path, err)
	}
	return cfg, nil
}

func (c Config) validate() error {
	if c.Retention.TraceDays < 0 {
		return fmt.Errorf("retention.traces_days must be >= 0 (0 keeps traces), got %d", c.Retention.TraceDays)
	}
	if c.Retention.EvaluationRunDays < 0 {
		return fmt.Errorf("retention.evaluation_runs_days must be >= 0 (0 keeps runs), got %d", c.Retention.EvaluationRunDays)
	}
	if c.Retention.AuditDays < 0 {
		return fmt.Errorf("retention.audit_days must be >= 0 (0 keeps audit events), got %d", c.Retention.AuditDays)
	}
	if _, err := outbound.ParsePolicy(c.Privacy.OutboundPolicy); err != nil {
		return fmt.Errorf("privacy.outbound_policy: %w", err)
	}
	if _, err := outbound.NewDetector(c.Privacy.Detectors(), c.Privacy.SensitiveRules); err != nil {
		return fmt.Errorf("privacy: %w", err)
	}
	for _, root := range c.Sources.Filesystem.AllowedRoots {
		if !filepath.IsAbs(root) {
			return fmt.Errorf("sources.filesystem.allowed_roots: %q must be an absolute path", root)
		}
	}
	if w := c.Sources.Filesystem.Workers; w < 0 || w > 64 {
		return fmt.Errorf("sources.filesystem.workers must be between 0 and 64, got %d", w)
	}
	if c.Sources.Filesystem.MaxFileBytes < 0 {
		return fmt.Errorf("sources.filesystem.max_file_bytes must be >= 0, got %d", c.Sources.Filesystem.MaxFileBytes)
	}
	switch c.Profile {
	case "", ProfileDevelopment, ProfileProduction:
	default:
		return fmt.Errorf("profile must be %q or %q, got %q", ProfileDevelopment, ProfileProduction, c.Profile)
	}
	switch c.Security.ManagementBoundary {
	case "", ManagementBoundaryReverseProxyIdentity:
	default:
		return fmt.Errorf("security.management_boundary must be \"\" or %q, got %q", ManagementBoundaryReverseProxyIdentity, c.Security.ManagementBoundary)
	}
	switch c.Security.StorageAtRest {
	case "", StorageAtRestOperatorEncryptedVolume:
	default:
		return fmt.Errorf("security.storage_at_rest must be \"\" or %q, got %q", StorageAtRestOperatorEncryptedVolume, c.Security.StorageAtRest)
	}
	return nil
}

func applyEnvOverrides(cfg *Config) {
	if v, ok := os.LookupEnv("FORGEAI_PORT"); ok {
		if port, err := strconv.Atoi(v); err == nil {
			cfg.Server.Port = port
		}
	}
	if v, ok := os.LookupEnv("FORGEAI_DB_PATH"); ok {
		cfg.Database.Path = v
	}
	if v, ok := os.LookupEnv("FORGEAI_STORAGE_PATH"); ok {
		cfg.Storage.Path = v
	}
	if v, ok := os.LookupEnv("FORGEAI_PRIVACY_MODE"); ok {
		cfg.Privacy.Mode = v
	}
	if v, ok := os.LookupEnv("FORGEAI_OUTBOUND_POLICY"); ok {
		cfg.Privacy.OutboundPolicy = v
	}
	if v, ok := os.LookupEnv("FORGEAI_PROFILE"); ok {
		cfg.Profile = v
	}
}

// EnsureDirs creates the parent directories for the database and file
// storage paths so a fresh checkout can `forgeai serve` without a manual
// `mkdir` step.
func (c Config) EnsureDirs() error {
	if c.Database.Type == "sqlite" {
		if err := os.MkdirAll(filepath.Dir(c.Database.Path), 0o755); err != nil {
			return fmt.Errorf("creating database directory: %w", err)
		}
	}
	if c.Storage.Type == "filesystem" {
		if err := os.MkdirAll(c.Storage.Path, 0o755); err != nil {
			return fmt.Errorf("creating storage directory: %w", err)
		}
	}
	return nil
}
