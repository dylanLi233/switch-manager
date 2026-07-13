package config

import "testing"

func TestLoadBackupEnvironmentDisabledByDefault(t *testing.T) {
	cfg, err := LoadBackupEnvironment(func(string) (string, bool) { return "", false })
	if err != nil || cfg.Enabled {
		t.Fatalf("cfg=%+v err=%v", cfg, err)
	}
}

func TestLoadBackupEnvironmentEnabled(t *testing.T) {
	env := map[string]string{BackupRootDirEnv: "/tmp/switch-manager-backups", BackupMaxFileBytesEnv: "4096"}
	cfg, err := LoadBackupEnvironment(func(key string) (string, bool) {
		value, ok := env[key]
		return value, ok
	})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled || cfg.RootDir != "/tmp/switch-manager-backups" || cfg.MaxFileBytes != 4096 {
		t.Fatalf("cfg=%+v", cfg)
	}
}

func TestLoadBackupEnvironmentRejectsLimitWithoutRoot(t *testing.T) {
	_, err := LoadBackupEnvironment(func(key string) (string, bool) {
		if key == BackupMaxFileBytesEnv {
			return "4096", true
		}
		return "", false
	})
	if err == nil {
		t.Fatal("expected error")
	}
}
