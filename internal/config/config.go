package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config represents all operational configuration options for Genesis
type Config struct {
	TCPAddr            string        `json:"tcp"`
	UDPAddr            string        `json:"udp"`
	SyslogAddr         string        `json:"syslog"`
	HTTPAddr           string        `json:"http"`
	AllowAllInterfaces bool          `json:"allow_all_interfaces"`
	MaxMemMB           int64         `json:"max_mem_mb"`
	MaxConns           int64         `json:"max_conns"`
	MaxLineLen         int           `json:"max_line_len"`
	DataDir            string        `json:"data_dir"`
	RetentionDuration  time.Duration `json:"retention_duration"`
	RetentionStr       string        `json:"retention"`
	MaxDiskBytes       int64         `json:"max_disk_bytes"`
	RedactEnabled      bool          `json:"redact"`
	CustomRedact       []string      `json:"custom_redact_patterns"`
	AuthToken          string        `json:"auth_token"`
	TLSCertFile        string        `json:"tls_cert"`
	TLSKeyFile         string        `json:"tls_key"`
}

// DefaultConfig returns safe production defaults
func DefaultConfig() *Config {
	return &Config{
		TCPAddr:            "127.0.0.1:9000",
		UDPAddr:            "127.0.0.1:9001",
		SyslogAddr:         "127.0.0.1:5514",
		HTTPAddr:           "127.0.0.1:8080",
		AllowAllInterfaces: false,
		MaxMemMB:           350,
		MaxConns:           20000,
		MaxLineLen:         65536,
		DataDir:            "", // In-memory only by default
		RetentionDuration:  24 * time.Hour,
		RetentionStr:       "24h",
		MaxDiskBytes:       10 * 1024 * 1024 * 1024, // 10 GB
		RedactEnabled:      false,
		AuthToken:          "",
		TLSCertFile:        "",
		TLSKeyFile:         "",
	}
}

// LoadConfig merges Defaults -> Config File -> Environment Variables
func LoadConfig(configPath string) (*Config, error) {
	cfg := DefaultConfig()

	// 1. Try loading from file
	if configPath != "" {
		if err := loadFromFile(cfg, configPath); err != nil {
			return nil, fmt.Errorf("failed to load config file %q: %w", configPath, err)
		}
	} else if _, err := os.Stat("genesis.json"); err == nil {
		_ = loadFromFile(cfg, "genesis.json")
	}

	// 2. Override with GENESIS_* Environment Variables
	loadFromEnv(cfg)

	// Parse retention string into duration if set
	if cfg.RetentionStr != "" {
		if d, err := time.ParseDuration(cfg.RetentionStr); err == nil {
			cfg.RetentionDuration = d
		}
	}

	return cfg, nil
}

func loadFromFile(cfg *Config, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, cfg)
}

func loadFromEnv(cfg *Config) {
	if v := os.Getenv("GENESIS_TCP"); v != "" {
		cfg.TCPAddr = v
	}
	if v := os.Getenv("GENESIS_UDP"); v != "" {
		cfg.UDPAddr = v
	}
	if v := os.Getenv("GENESIS_SYSLOG"); v != "" {
		cfg.SyslogAddr = v
	}
	if v := os.Getenv("GENESIS_HTTP"); v != "" {
		cfg.HTTPAddr = v
	}
	if v := os.Getenv("GENESIS_ALLOW_ALL_INTERFACES"); v != "" {
		cfg.AllowAllInterfaces = parseBool(v)
	}
	if v := os.Getenv("GENESIS_MAX_MEM_MB"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			cfg.MaxMemMB = n
		}
	}
	if v := os.Getenv("GENESIS_MAX_CONNS"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			cfg.MaxConns = n
		}
	}
	if v := os.Getenv("GENESIS_MAX_LINE_LEN"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.MaxLineLen = n
		}
	}
	if v := os.Getenv("GENESIS_DATA_DIR"); v != "" {
		cfg.DataDir = v
	}
	if v := os.Getenv("GENESIS_RETENTION"); v != "" {
		cfg.RetentionStr = v
	}
	if v := os.Getenv("GENESIS_MAX_DISK_BYTES"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			cfg.MaxDiskBytes = n
		}
	}
	if v := os.Getenv("GENESIS_REDACT"); v != "" {
		cfg.RedactEnabled = parseBool(v)
	}
	if v := os.Getenv("GENESIS_CUSTOM_REDACT"); v != "" {
		cfg.CustomRedact = strings.Split(v, ",")
	}
	if v := os.Getenv("GENESIS_AUTH_TOKEN"); v != "" {
		cfg.AuthToken = v
	}
	if v := os.Getenv("GENESIS_TLS_CERT"); v != "" {
		cfg.TLSCertFile = v
	}
	if v := os.Getenv("GENESIS_TLS_KEY"); v != "" {
		cfg.TLSKeyFile = v
	}
}

func parseBool(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return s == "true" || s == "1" || s == "yes" || s == "on"
}
