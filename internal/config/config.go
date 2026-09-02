// Package config loads ForgeAI's server configuration from a YAML file,
// then applies FORGEAI_* environment variable overrides on top.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server    ServerConfig    `yaml:"server"`
	Database  DatabaseConfig  `yaml:"database"`
	Storage   StorageConfig   `yaml:"storage"`
	Security  SecurityConfig  `yaml:"security"`
	LLM       LLMConfig       `yaml:"llm"`
	Embedding EmbeddingConfig `yaml:"embedding"`
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

	return cfg, nil
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
