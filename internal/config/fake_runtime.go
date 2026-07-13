package config

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

const (
	FakeRuntimeEnabledEnv = "SWITCH_MANAGER_FAKE_PLUGIN_ENABLED"
	OperationWorkersEnv   = "SWITCH_MANAGER_OPERATION_WORKERS"
	OperationSyncWaitEnv  = "SWITCH_MANAGER_OPERATION_SYNC_WAIT_TIMEOUT"
)

type FakeRuntimeConfig struct {
	Enabled         bool
	Workers         int
	SyncWaitTimeout time.Duration
}

func LoadFakeRuntimeEnvironment(lookup LookupEnv) (FakeRuntimeConfig, error) {
	if lookup == nil {
		return FakeRuntimeConfig{}, errors.New("environment lookup is required")
	}
	cfg := FakeRuntimeConfig{Workers: 5, SyncWaitTimeout: 10 * time.Second}
	if raw, ok := lookup(FakeRuntimeEnabledEnv); ok {
		enabled, err := strconv.ParseBool(strings.TrimSpace(raw))
		if err != nil {
			return FakeRuntimeConfig{}, err
		}
		cfg.Enabled = enabled
	}
	if raw, ok := lookup(OperationWorkersEnv); ok {
		workers, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || workers < 1 || workers > 32 {
			return FakeRuntimeConfig{}, errors.New("operation workers must be between 1 and 32")
		}
		cfg.Workers = workers
	}
	if raw, ok := lookup(OperationSyncWaitEnv); ok {
		wait, err := time.ParseDuration(strings.TrimSpace(raw))
		if err != nil || wait <= 0 || wait > time.Minute {
			return FakeRuntimeConfig{}, errors.New("operation sync wait timeout must be between 0 and 1 minute")
		}
		cfg.SyncWaitTimeout = wait
	}
	return cfg, nil
}
