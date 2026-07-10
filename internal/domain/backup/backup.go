// Package backup defines immutable configuration backup metadata.
package backup

import (
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/dylanLi233/switch-manager/internal/domain/device"
)

// ConfigBackup identifies one stored switch configuration snapshot.
type ConfigBackup struct {
	ID            string
	DeviceID      string
	Vendor        device.Vendor
	Model         string
	OSVersion     string
	PluginName    string
	PluginVersion string
	FilePath      string
	SHA256        string
	FileSize      int64
	CreatedBy     string
	TaskID        string
	CreatedAt     time.Time
}

// Validate enforces safe relative paths and integrity metadata.
func (b ConfigBackup) Validate() error {
	if strings.TrimSpace(b.ID) == "" || strings.TrimSpace(b.DeviceID) == "" {
		return errors.New("backup ID and device ID are required")
	}
	if err := b.Vendor.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(b.PluginName) == "" || strings.TrimSpace(b.PluginVersion) == "" {
		return errors.New("backup plugin name and version are required")
	}
	if strings.TrimSpace(b.FilePath) == "" {
		return errors.New("backup file path is required")
	}
	clean := filepath.Clean(b.FilePath)
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return errors.New("backup file path must stay within the configured root")
	}
	if b.SHA256 == "" || len(b.SHA256) != 64 {
		return errors.New("backup SHA-256 must contain 64 hexadecimal characters")
	}
	decoded, err := hex.DecodeString(b.SHA256)
	if err != nil || len(decoded) != 32 {
		return fmt.Errorf("backup SHA-256 is invalid")
	}
	if b.FileSize < 0 {
		return errors.New("backup file size cannot be negative")
	}
	if strings.TrimSpace(b.CreatedBy) == "" || strings.TrimSpace(b.TaskID) == "" {
		return errors.New("backup creator and task ID are required")
	}
	if b.CreatedAt.IsZero() {
		return errors.New("backup created time is required")
	}
	return nil
}
