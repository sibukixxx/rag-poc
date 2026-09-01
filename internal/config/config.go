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
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	Storage  StorageConfig  `yaml:"storage"`
	Security SecurityConfig `yaml:"security"`
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
