package backup

import (
	"strings"
	"testing"
	"time"

	"github.com/dylanLi233/switch-manager/internal/domain/device"
)

func validBackup() ConfigBackup {
	return ConfigBackup{
		ID: "backup-1", DeviceID: "sw-1", Vendor: device.VendorHuawei,
		PluginName: "huawei", PluginVersion: "1.0.0",
		FilePath: "sw-1/2026/07/backup-1.txt", SHA256: strings.Repeat("a", 64),
		FileSize: 1024, CreatedBy: "user-1", TaskID: "task-1", CreatedAt: time.Now().UTC(),
	}
}

func TestConfigBackupValidate(t *testing.T) {
	t.Parallel()
	if err := validBackup().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	for _, path := range []string{"../secret", "/etc/passwd"} {
		b := validBackup()
		b.FilePath = path
		if err := b.Validate(); err == nil {
			t.Fatalf("expected unsafe path %q to fail", path)
		}
	}

	b := validBackup()
	b.SHA256 = strings.Repeat("z", 64)
	if err := b.Validate(); err == nil {
		t.Fatal("expected invalid SHA-256 error")
	}
}
