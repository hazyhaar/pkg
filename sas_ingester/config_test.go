package sas_ingester

import (
	"os"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default config should be valid: %v", err)
	}
	if cfg.MaxFileBytes() != 500*1024*1024 {
		t.Errorf("MaxFileBytes = %d", cfg.MaxFileBytes())
	}
}

func TestLoadConfig(t *testing.T) {
	yaml := `
listen: ":9090"
db_path: "/tmp/test.db"
chunks_dir: "/tmp/chunks"
max_file_mb: 100
chunk_size_mb: 5
clamav:
  enabled: false
webhooks:
  - name: "test"
    url: "https://example.com/hook"
    auth_mode: "opaque_only"
    secret: "webhook-hmac-key"
jwt_secret: "secret123"
`
	f, err := os.CreateTemp("", "config_test_*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.WriteString(yaml)
	f.Close()

	cfg, err := LoadConfig(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != ":9090" {
		t.Errorf("Listen = %q", cfg.Listen)
	}
	if cfg.MaxFileMB != 100 {
		t.Errorf("MaxFileMB = %d", cfg.MaxFileMB)
	}
	if len(cfg.Webhooks) != 1 {
		t.Errorf("Webhooks len = %d", len(cfg.Webhooks))
	}
}

func TestValidate_BadAuthMode(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Webhooks = []WebhookTarget{{URL: "http://x", AuthMode: "invalid"}}
	if err := cfg.Validate(); err == nil {
		t.Error("expected validation error for invalid auth_mode")
	}
}

func TestValidate_MissingURL(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Webhooks = []WebhookTarget{{AuthMode: "opaque_only"}}
	if err := cfg.Validate(); err == nil {
		t.Error("expected validation error for missing url")
	}
}

func TestValidate_BadAuthMode_Legacy(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Webhooks = []WebhookTarget{{URL: "http://x", AuthMode: "bearer"}}
	if err := cfg.Validate(); err == nil {
		t.Error("expected validation error for legacy bearer auth_mode")
	}
}
