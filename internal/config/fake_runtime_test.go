package config

import (
	"testing"
	"time"
)

func TestFakeRuntimeDefaultsDisabled(t *testing.T) {
	cfg, err := LoadFakeRuntimeEnvironment(func(string) (string, bool) { return "", false })
	if err != nil || cfg.Enabled || cfg.Workers != 5 || cfg.SyncWaitTimeout != 10*time.Second {
		t.Fatalf("cfg=%+v err=%v", cfg, err)
	}
}

func TestFakeRuntimeEnvironment(t *testing.T) {
	env := map[string]string{FakeRuntimeEnabledEnv: "true", OperationWorkersEnv: "3", OperationSyncWaitEnv: "4s"}
	cfg, err := LoadFakeRuntimeEnvironment(func(key string) (string, bool) { value, ok := env[key]; return value, ok })
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled || cfg.Workers != 3 || cfg.SyncWaitTimeout != 4*time.Second {
		t.Fatalf("cfg=%+v", cfg)
	}
}

func TestFakeRuntimeRejectsInvalidLimits(t *testing.T) {
	for key, value := range map[string]string{OperationWorkersEnv: "0", OperationSyncWaitEnv: "0s", FakeRuntimeEnabledEnv: "maybe"} {
		_, err := LoadFakeRuntimeEnvironment(func(candidate string) (string, bool) {
			if candidate == key { return value, true }
			return "", false
		})
		if err == nil {
			t.Fatalf("%s=%s expected error", key, value)
		}
	}
}
