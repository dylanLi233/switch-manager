package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadAuthenticationConfiguration(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := `database:
  dsn: "postgres://db"
authentication:
  enabled: true
  issuer: "ops-platform"
  audience: "switch-manager"
  public_key_file: "/etc/switch-manager/jwt.pem"
  key_id: "key-1"
  clock_skew: "45s"
  username_claim: "preferred_username"
  service_actor_claim: "azp"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.Authentication.Enabled || cfg.Authentication.Issuer != "ops-platform" || cfg.Authentication.Audience != "switch-manager" {
		t.Fatalf("authentication = %+v", cfg.Authentication)
	}
	if cfg.Authentication.ClockSkew != 45*time.Second || cfg.Authentication.KeyID != "key-1" {
		t.Fatalf("authentication = %+v", cfg.Authentication)
	}
}

func TestAuthenticationEnabledRequiresCompleteConfiguration(t *testing.T) {
	t.Parallel()
	cfg := Default()
	cfg.Authentication.Enabled = true
	for _, mutate := range []func(*Config){
		func(c *Config) { c.Authentication.Issuer = "issuer"; c.Authentication.Audience = "audience"; c.Authentication.PublicKeyFile = "key.pem" },
		func(c *Config) { c.Database.DSN = "postgres://db"; c.Authentication.Audience = "audience"; c.Authentication.PublicKeyFile = "key.pem" },
		func(c *Config) { c.Database.DSN = "postgres://db"; c.Authentication.Issuer = "issuer"; c.Authentication.PublicKeyFile = "key.pem" },
		func(c *Config) { c.Database.DSN = "postgres://db"; c.Authentication.Issuer = "issuer"; c.Authentication.Audience = "audience" },
	} {
		candidate := cfg
		mutate(&candidate)
		if err := candidate.Validate(); err == nil {
			t.Fatalf("expected validation error for %+v", candidate.Authentication)
		}
	}
}

func TestAuthenticationEnvironmentOverride(t *testing.T) {
	t.Parallel()
	env := map[string]string{
		"SWITCH_MANAGER_DATABASE_DSN":             "postgres://db",
		"SWITCH_MANAGER_AUTH_ENABLED":             "true",
		"SWITCH_MANAGER_AUTH_ISSUER":              "issuer",
		"SWITCH_MANAGER_AUTH_AUDIENCE":            "audience",
		"SWITCH_MANAGER_AUTH_PUBLIC_KEY_FILE":     "key.pem",
		"SWITCH_MANAGER_AUTH_CLOCK_SKEW":          "20s",
		"SWITCH_MANAGER_AUTH_USERNAME_CLAIM":      "user_name",
		"SWITCH_MANAGER_AUTH_SERVICE_ACTOR_CLAIM": "client_id",
	}
	cfg, err := Load("", func(key string) (string, bool) { value, ok := env[key]; return value, ok })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Authentication.ClockSkew != 20*time.Second || cfg.Authentication.UsernameClaim != "user_name" || cfg.Authentication.ServiceActorClaim != "client_id" {
		t.Fatalf("authentication = %+v", cfg.Authentication)
	}
}
