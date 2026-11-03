package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestConfig_Defaults(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.TCPAddr != "127.0.0.1:9000" {
		t.Errorf("expected default TCP 127.0.0.1:9000, got %s", cfg.TCPAddr)
	}
	if cfg.MaxMemMB != 350 {
		t.Errorf("expected default MaxMemMB 350, got %d", cfg.MaxMemMB)
	}
	if cfg.DataDir != "" {
		t.Errorf("expected default DataDir empty (in-memory only), got %s", cfg.DataDir)
	}
}

func TestConfig_FileLoading(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "genesis.json")
	content := `{
		"tcp": "127.0.0.1:9999",
		"http": "127.0.0.1:8888",
		"max_mem_mb": 500,
		"retention": "48h",
		"redact": true,
		"auth_token": "secret-token-123"
	}`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test config file: %v", err)
	}

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if cfg.TCPAddr != "127.0.0.1:9999" {
		t.Errorf("expected TCP 127.0.0.1:9999, got %s", cfg.TCPAddr)
	}
	if cfg.HTTPAddr != "127.0.0.1:8888" {
		t.Errorf("expected HTTP 127.0.0.1:8888, got %s", cfg.HTTPAddr)
	}
	if cfg.MaxMemMB != 500 {
		t.Errorf("expected MaxMemMB 500, got %d", cfg.MaxMemMB)
	}
	if cfg.RetentionDuration != 48*time.Hour {
		t.Errorf("expected RetentionDuration 48h, got %v", cfg.RetentionDuration)
	}
	if !cfg.RedactEnabled {
		t.Errorf("expected RedactEnabled true")
	}
	if cfg.AuthToken != "secret-token-123" {
		t.Errorf("expected AuthToken 'secret-token-123', got %s", cfg.AuthToken)
	}
}

func TestConfig_EnvOverride(t *testing.T) {
	t.Setenv("GENESIS_TCP", "127.0.0.1:9111")
	t.Setenv("GENESIS_MAX_MEM_MB", "750")
	t.Setenv("GENESIS_REDACT", "true")
	t.Setenv("GENESIS_DATA_DIR", "/tmp/genesis-data")

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if cfg.TCPAddr != "127.0.0.1:9111" {
		t.Errorf("expected TCP from env 127.0.0.1:9111, got %s", cfg.TCPAddr)
	}
	if cfg.MaxMemMB != 750 {
		t.Errorf("expected MaxMemMB from env 750, got %d", cfg.MaxMemMB)
	}
	if !cfg.RedactEnabled {
		t.Errorf("expected RedactEnabled from env true")
	}
	if cfg.DataDir != "/tmp/genesis-data" {
		t.Errorf("expected DataDir from env '/tmp/genesis-data', got %s", cfg.DataDir)
	}
}
