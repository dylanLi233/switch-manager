package config

import (
	"bytes"
	"encoding/base64"
	"testing"
	"time"
)

func TestLoadInventoryEnvironmentDisabledWithoutKey(t *testing.T) {
	cfg, err := LoadInventoryEnvironment(func(string) (string, bool) { return "", false })
	if err != nil || cfg.Enabled {
		t.Fatalf("cfg=%+v err=%v", cfg, err)
	}
}

func TestLoadInventoryEnvironmentValidatesKeyAndSSH(t *testing.T) {
	env := map[string]string{
		InventoryMasterKeyEnv:     base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)),
		InventoryKeyVersionEnv:    "key-7",
		InventoryKnownHostsEnv:    "known_hosts",
		InventoryPromptPatternEnv: `FAKE> $`,
		InventoryPromptFamilyEnv:  "fake",
		InventorySSHTimeoutEnv:    "3s",
	}
	cfg, err := LoadInventoryEnvironment(func(key string) (string, bool) {
		value, ok := env[key]
		return value, ok
	})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled || cfg.KeyVersion != "key-7" || cfg.SSHTimeout != 3*time.Second {
		t.Fatalf("cfg=%+v", cfg)
	}
}

func TestLoadInventoryEnvironmentRejectsShortKey(t *testing.T) {
	_, err := LoadInventoryEnvironment(func(key string) (string, bool) {
		if key == InventoryMasterKeyEnv {
			return base64.StdEncoding.EncodeToString([]byte("short")), true
		}
		return "", false
	})
	if err == nil {
		t.Fatal("expected error")
	}
}
