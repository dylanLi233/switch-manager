package postgres

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/dylanLi233/switch-manager/internal/domain/credential"
	"github.com/dylanLi233/switch-manager/internal/domain/device"
	"github.com/dylanLi233/switch-manager/internal/inventorysvc"
	"github.com/dylanLi233/switch-manager/internal/secretbox"
)

type integrationProbe struct{}

func (integrationProbe) Test(context.Context, device.Device, inventorysvc.AuthenticationMaterial) (inventorysvc.ConnectionTestResult, error) {
	return inventorysvc.ConnectionTestResult{TCPConnected: true, SSHNegotiated: true, Authenticated: true}, nil
}

type integrationDetector struct{}

func (integrationDetector) Detect(context.Context, device.Device, inventorysvc.AuthenticationMaterial) (inventorysvc.DetectionResult, error) {
	return inventorysvc.DetectionResult{Vendor: device.VendorH3C, Model: "S5130", OSVersion: "7.1", EvidenceSummary: "fixture", Capabilities: []string{"diagnostic.echo"}}, nil
}

func TestInventoryServicePostgreSQLIntegration(t *testing.T) {
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
		t.Fatal(err)
	}
	defer store.Close()
	repos := store.Repositories()
	box, err := secretbox.New(bytes.Repeat([]byte{9}, secretbox.KeyBytes), "integration-v1")
	if err != nil {
		t.Fatal(err)
	}
	service, err := inventorysvc.New(repos.Devices, repos.Credentials, repos.ExecutionCredentials, box, integrationProbe{}, integrationDetector{})
	if err != nil {
		t.Fatal(err)
	}

	credentialView, err := service.CreateCredential(ctx, inventorysvc.CredentialInput{Name: "integration-admin", Type: credential.TypePassword, Username: "admin", Password: "plain-database-secret"})
	if err != nil {
		t.Fatal(err)
	}
	var encrypted []byte
	if err := store.pool.QueryRow(ctx, `SELECT encrypted_secret FROM credentials WHERE id=$1::uuid`, credentialView.ID).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	if len(encrypted) == 0 || bytes.Contains(encrypted, []byte("plain-database-secret")) {
		t.Fatalf("credential ciphertext is unsafe: %q", encrypted)
	}

	first, err := service.CreateDevice(ctx, inventorysvc.DeviceInput{Name: "core-switch", Host: "192.0.2.50", SSHPort: 22, CredentialID: credentialView.ID, Vendor: device.VendorHuawei})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateDevice(ctx, inventorysvc.DeviceInput{Name: "duplicate", Host: "192.0.2.50", SSHPort: 22, CredentialID: credentialView.ID, Vendor: device.VendorHuawei}); !apperror.IsCode(err, apperror.CodeStateConflict) {
		t.Fatalf("duplicate host+port error=%v", err)
	}
	listed, err := service.ListDevices(ctx, device.ListFilter{Keyword: "core", Limit: 10})
	if err != nil || len(listed) != 1 || listed[0].ID != first.ID {
		t.Fatalf("listed=%+v err=%v", listed, err)
	}

	detected, err := service.Detect(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detected.Device.IdentityStatus != device.IdentityMismatch || detected.Device.Model != "S5130" {
		t.Fatalf("detected=%+v", detected)
	}
	if err := service.DeleteCredential(ctx, credentialView.ID); !apperror.IsCode(err, apperror.CodeStateConflict) {
		t.Fatalf("referenced credential delete error=%v", err)
	}
	if err := service.DeleteDevice(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteCredential(ctx, credentialView.ID); err != nil {
		t.Fatal(err)
	}
}
