package postgres

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/dylanLi233/switch-manager/internal/domain/credential"
	"github.com/dylanLi233/switch-manager/internal/domain/device"
)

func TestCredentialSoftDeleteGuardIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("TEST_DATABASE_DSN is not set")
	}
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	runMigration(t, root, dsn, "down", "all")
	runMigration(t, root, dsn, "up")

	ctx := context.Background()
	store, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()
	repos := store.Repositories()
	now := time.Now().UTC().Truncate(time.Microsecond)

	credentialID := "00000000-0000-0000-0000-000000000201"
	deviceID := "00000000-0000-0000-0000-000000000202"
	if _, err := repos.Credentials.Create(ctx, credential.Credential{
		ID: credentialID, Name: "guarded-password", Type: credential.TypePassword,
		Username: "admin", EncryptedSecret: []byte("encrypted-secret"),
		KeyVersion: "v1", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("Create credential: %v", err)
	}
	if _, err := repos.Devices.Create(ctx, device.Device{
		ID: deviceID, Name: "guarded-switch", Host: "192.0.2.202", SSHPort: 22,
		CredentialID: credentialID, Vendor: device.VendorH3C,
		DetectMode: device.DetectModeAuto, IdentityStatus: device.IdentityVerified,
		Status: device.StatusActive, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("Create device: %v", err)
	}

	if err := repos.Credentials.SoftDelete(ctx, credentialID); !apperror.IsCode(err, apperror.CodeStateConflict) {
		t.Fatalf("credential in-use delete error = %v", err)
	}
	if err := repos.Devices.SoftDelete(ctx, deviceID); err != nil {
		t.Fatalf("SoftDelete device: %v", err)
	}
	if err := repos.Credentials.SoftDelete(ctx, credentialID); err != nil {
		t.Fatalf("SoftDelete unused credential: %v", err)
	}
	if _, err := repos.ExecutionCredentials.GetForExecution(ctx, credentialID); !apperror.IsCode(err, apperror.CodeCredentialNotFound) {
		t.Fatalf("deleted credential execution lookup error = %v", err)
	}
}
