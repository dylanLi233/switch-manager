package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadFileAndEnvironmentOverride(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `server:
  listen: "127.0.0.1:9000"
  read_timeout: "5s"
  write_timeout: "20s"
  shutdown_timeout: "3s"
  max_request_bytes: 2048
database:
  required: true
  dsn: "from-file"
logging:
  level: "warn"
  format: "text"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	env := map[string]string{
		"SWITCH_MANAGER_SERVER_LISTEN": "127.0.0.1:9100",
		"SWITCH_MANAGER_DATABASE_DSN":  "from-env",
	}
	cfg, err := Load(path, func(key string) (string, bool) {
		value, ok := env[key]
		return value, ok
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Server.Listen != "127.0.0.1:9100" {
		t.Fatalf("listen = %q", cfg.Server.Listen)
	}
	if cfg.Server.ReadTimeout != 5*time.Second {
		t.Fatalf("read timeout = %s", cfg.Server.ReadTimeout)
	}
	if cfg.Database.DSN != "from-env" || !cfg.Database.Required {
		t.Fatalf("database config = %+v", cfg.Database)
	}
	if cfg.Logging.Level != "warn" || cfg.Logging.Format != "text" {
		t.Fatalf("logging config = %+v", cfg.Logging)
	}
}

func TestLoadRejectsUnknownKey(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("server:\n  mystery: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path, func(string) (string, bool) { return "", false })
	if err == nil || !strings.Contains(err.Error(), "unknown config key") {
		t.Fatalf("expected unknown key error, got %v", err)
	}
}

func TestLoadRejectsInvalidDuration(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("server:\n  read_timeout: never\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path, func(string) (string, bool) { return "", false })
	if err == nil || !strings.Contains(err.Error(), "server.read_timeout") {
		t.Fatalf("expected duration error, got %v", err)
	}
}

func TestValidateRequiresDSNWhenDatabaseRequired(t *testing.T) {
	t.Parallel()

	cfg := Default()
	cfg.Database.Required = true
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error")
	}
}
