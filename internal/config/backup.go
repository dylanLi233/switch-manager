package config

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	BackupRootDirEnv      = "SWITCH_MANAGER_BACKUP_ROOT_DIR"
	BackupMaxFileBytesEnv = "SWITCH_MANAGER_BACKUP_MAX_FILE_BYTES"
)

const defaultBackupMaxFileBytes int64 = 10 << 20

// BackupConfig is intentionally environment-only until backup file encryption
// and key management are decided. An empty root keeps the subsystem disabled.
type BackupConfig struct {
	Enabled      bool
	RootDir      string
	MaxFileBytes int64
}

func LoadBackupEnvironment(lookup LookupEnv) (BackupConfig, error) {
	if lookup == nil {
		return BackupConfig{}, errors.New("environment lookup is required")
	}
	cfg := BackupConfig{MaxFileBytes: defaultBackupMaxFileBytes}
	if value, ok := lookup(BackupRootDirEnv); ok {
		cfg.RootDir = strings.TrimSpace(value)
	}
	if value, ok := lookup(BackupMaxFileBytesEnv); ok {
		parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil {
			return BackupConfig{}, fmt.Errorf("parse %s: %w", BackupMaxFileBytesEnv, err)
		}
		cfg.MaxFileBytes = parsed
	}
	cfg.Enabled = cfg.RootDir != ""
	if !cfg.Enabled {
		if _, ok := lookup(BackupMaxFileBytesEnv); ok {
			return BackupConfig{}, errors.New("backup root directory is required when backup max file bytes is configured")
		}
		return cfg, nil
	}
	if cfg.MaxFileBytes < 1 || cfg.MaxFileBytes > 1<<30 {
		return BackupConfig{}, errors.New("backup max file bytes must be between 1 and 1073741824")
	}
	if strings.ContainsAny(cfg.RootDir, "\x00\r\n") {
		return BackupConfig{}, errors.New("backup root directory is invalid")
	}
	return cfg, nil
}
