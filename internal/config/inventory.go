package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	InventoryMasterKeyEnv     = "SWITCH_MANAGER_CREDENTIAL_MASTER_KEY_BASE64"
	InventoryKeyVersionEnv    = "SWITCH_MANAGER_CREDENTIAL_KEY_VERSION"
	InventoryKnownHostsEnv    = "SWITCH_MANAGER_SSH_KNOWN_HOSTS_FILE"
	InventoryPromptPatternEnv = "SWITCH_MANAGER_SSH_PROMPT_PATTERN"
	InventoryPromptFamilyEnv  = "SWITCH_MANAGER_SSH_PROMPT_FAMILY"
	InventorySSHTimeoutEnv    = "SWITCH_MANAGER_SSH_TEST_TIMEOUT"
)

type InventoryConfig struct {
	Enabled         bool
	MasterKeyBase64 string
	KeyVersion      string
	KnownHostsFile  string
	PromptPattern   string
	PromptFamily    string
	SSHTimeout      time.Duration
}

// LoadInventoryEnvironment keeps credential master-key material out of YAML files.
func LoadInventoryEnvironment(lookup LookupEnv) (InventoryConfig, error) {
	if lookup == nil {
		return InventoryConfig{}, errors.New("environment lookup is required")
	}
	cfg := InventoryConfig{KeyVersion: "v1", SSHTimeout: 10 * time.Second}
	if value, ok := lookup(InventoryMasterKeyEnv); ok {
		cfg.MasterKeyBase64 = strings.TrimSpace(value)
	}
	if value, ok := lookup(InventoryKeyVersionEnv); ok {
		cfg.KeyVersion = strings.TrimSpace(value)
	}
	if value, ok := lookup(InventoryKnownHostsEnv); ok {
		cfg.KnownHostsFile = strings.TrimSpace(value)
	}
	if value, ok := lookup(InventoryPromptPatternEnv); ok {
		cfg.PromptPattern = value
	}
	if value, ok := lookup(InventoryPromptFamilyEnv); ok {
		cfg.PromptFamily = strings.TrimSpace(value)
	}
	if value, ok := lookup(InventorySSHTimeoutEnv); ok {
		timeout, err := time.ParseDuration(strings.TrimSpace(value))
		if err != nil {
			return InventoryConfig{}, fmt.Errorf("parse %s: %w", InventorySSHTimeoutEnv, err)
		}
		cfg.SSHTimeout = timeout
	}
	cfg.Enabled = cfg.MasterKeyBase64 != ""
	if !cfg.Enabled {
		if cfg.KnownHostsFile != "" || cfg.PromptPattern != "" || cfg.PromptFamily != "" {
			return InventoryConfig{}, errors.New("credential master key is required when SSH inventory settings are configured")
		}
		return cfg, nil
	}
	raw, err := base64.StdEncoding.DecodeString(cfg.MasterKeyBase64)
	if err != nil {
		return InventoryConfig{}, fmt.Errorf("decode credential master key: %w", err)
	}
	if len(raw) != 32 {
		return InventoryConfig{}, errors.New("credential master key must decode to 32 bytes")
	}
	if cfg.KeyVersion == "" {
		return InventoryConfig{}, errors.New("credential key version is required")
	}
	if cfg.SSHTimeout <= 0 || cfg.SSHTimeout > time.Minute {
		return InventoryConfig{}, errors.New("SSH test timeout must be between 0 and 1 minute")
	}
	if cfg.PromptPattern != "" && cfg.KnownHostsFile == "" {
		return InventoryConfig{}, errors.New("known_hosts file is required when prompt probing is configured")
	}
	return cfg, nil
}
