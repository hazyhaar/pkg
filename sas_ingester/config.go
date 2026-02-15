package sas_ingester

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config holds the full sas_ingester configuration.
type Config struct {
	Listen     string          `yaml:"listen"`
	DBPath     string          `yaml:"db_path"`
	ChunksDir  string          `yaml:"chunks_dir"`
	MaxFileMB  int             `yaml:"max_file_mb"`
	ChunkSizeMB int           `yaml:"chunk_size_mb"`
	ClamAV     ClamAVConfig    `yaml:"clamav"`
	Webhooks   []WebhookTarget `yaml:"webhooks"`
	JWTSecret  string          `yaml:"jwt_secret"`
}

// ClamAVConfig configures the ClamAV scanner.
type ClamAVConfig struct {
	Enabled    bool   `yaml:"enabled"`
	SocketPath string `yaml:"socket_path"`
}

// WebhookTarget configures a downstream webhook.
type WebhookTarget struct {
	Name          string `yaml:"name"`
	URL           string `yaml:"url"`
	AuthMode      string `yaml:"auth_mode"`      // none | bearer | hmac
	Secret        string `yaml:"secret"`          // per-webhook secret (bearer token or HMAC key)
	RequireReview bool   `yaml:"require_review"`
}

// DefaultConfig returns sane defaults.
func DefaultConfig() *Config {
	return &Config{
		Listen:      ":8081",
		DBPath:      "sas_ingester.db",
		ChunksDir:   "chunks",
		MaxFileMB:   500,
		ChunkSizeMB: 10,
		ClamAV: ClamAVConfig{
			Enabled:    false,
			SocketPath: "/var/run/clamav/clamd.ctl",
		},
	}
}

// LoadConfig reads and parses a YAML config file. Returns DefaultConfig merged with the file.
func LoadConfig(path string) (*Config, error) {
	cfg := DefaultConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	return cfg, cfg.Validate()
}

// Validate checks that required fields are present and values are sane.
func (c *Config) Validate() error {
	if c.DBPath == "" {
		return fmt.Errorf("db_path is required")
	}
	if c.ChunksDir == "" {
		return fmt.Errorf("chunks_dir is required")
	}
	if c.MaxFileMB <= 0 {
		return fmt.Errorf("max_file_mb must be > 0")
	}
	if c.ChunkSizeMB <= 0 {
		return fmt.Errorf("chunk_size_mb must be > 0")
	}
	for i, wh := range c.Webhooks {
		if wh.URL == "" {
			return fmt.Errorf("webhook[%d]: url is required", i)
		}
		switch wh.AuthMode {
		case "none", "":
			// OK, no secret needed.
		case "bearer", "hmac":
			if wh.Secret == "" {
				return fmt.Errorf("webhook[%d]: auth_mode %q requires a per-webhook secret", i, wh.AuthMode)
			}
		default:
			return fmt.Errorf("webhook[%d]: unsupported auth_mode %q", i, wh.AuthMode)
		}
	}
	return nil
}

// MaxFileBytes returns max file size in bytes.
func (c *Config) MaxFileBytes() int64 { return int64(c.MaxFileMB) * 1024 * 1024 }

// ChunkSizeBytes returns chunk size in bytes.
func (c *Config) ChunkSizeBytes() int64 { return int64(c.ChunkSizeMB) * 1024 * 1024 }
