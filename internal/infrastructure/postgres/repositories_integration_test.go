package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/dylanLi233/switch-manager/internal/domain/audit"
	"github.com/dylanLi233/switch-manager/internal/domain/credential"
	"github.com/dylanLi233/switch-manager/internal/domain/device"
	"github.com/dylanLi233/switch-manager/internal/domain/operation"
	"github.com/dylanLi233/switch-manager/internal/domain/task"
)

const (
	testUserID       = "00000000-0000-0000-0000-000000000101"
	testCredentialID = "00000000-0000-0000-0000-000000000102"
	testDeviceID     = "00000000-0000-0000-0000-000000000103"
	testTaskID       = "00000000-0000-0000-0000-000000000104"
	testAuditID      = "00000000-0000-0000-0000-000000000105"
)

func TestRepositoriesIntegration(t *testing.T) {
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
	if err := store.Ping(ctx); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO users(id, external_subject, username) VALUES ($1::uuid,$2,$3)`, testUserID, "repo-test", "repo-test"); err != nil {
		t.Fatalf("insert test user: %v", err)
	}

	repos := store.Repositories()
	now := time.Now().UTC().Truncate(time.Microsecond)
	metadata, err := repos.Credentials.Create(ctx, credential.Credential{
		ID: testCredentialID, Name: "repo-password", Type: credential.TypePassword,
		Username: "admin", EncryptedSecret: []byte("encrypted-secret"),
		KeyVersion: "v1", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("Create credential: %v", err)
	}
	if metadata.ID != testCredentialID || metadata.Username != "admin" {
		t.Fatalf("unexpected metadata: %+v", metadata)
	}
	executionCredential, err := repos.ExecutionCredentials.GetForExecution(ctx, testCredentialID)
	if err != nil || string(executionCredential.EncryptedSecret) != "encrypted-secret" {
		t.Fatalf("GetForExecution() = %+v, %v", executionCredential, err)
	}

	createdDevice, err := repos.Devices.Create(ctx, device.Device{
		ID: testDeviceID, Name: "repo-switch", Host: "192.0.2.101", SSHPort: 22,
		CredentialID: testCredentialID, Vendor: device.VendorHuawei,
		DetectMode: device.DetectModeAuto, IdentityStatus: device.IdentityVerified,
		Status: device.StatusActive, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil || createdDevice.ID != testDeviceID {
		t.Fatalf("Create device = %+v, %v", createdDevice, err)
	}
	listed, err := repos.Devices.List(ctx, device.ListFilter{Limit: 10})
	if err != nil || len(listed) != 1 {
		t.Fatalf("List devices = %d, %v", len(listed), err)
	}

	rollbackSentinel := errors.New("force rollback")
	rollbackDeviceID := "00000000-0000-0000-0000-000000000106"
	err = store.WithinTx(ctx, func(txRepos *Repositories) error {
		_, createErr := txRepos.Devices.Create(ctx, device.Device{
			ID: rollbackDeviceID, Name: "rollback-switch", Host: "192.0.2.106", SSHPort: 22,
			CredentialID: testCredentialID, Vendor: device.VendorH3C,
			DetectMode: device.DetectModeAuto, IdentityStatus: device.IdentityUnknown,
			Status: device.StatusActive, CreatedAt: now, UpdatedAt: now,
		})
		if createErr != nil {
			return createErr
		}
		return rollbackSentinel
	})
	if !errors.Is(err, rollbackSentinel) {
		t.Fatalf("WithinTx() error = %v", err)
	}
	if _, err := repos.Devices.Get(ctx, rollbackDeviceID); !apperror.IsCode(err, apperror.CodeDeviceNotFound) {
		t.Fatalf("rolled back device error = %v", err)
	}

	createdTask, err := repos.Tasks.Create(ctx, task.Persisted{
		Task: task.Task{
			ID: testTaskID, Type: task.TypeOperation, Operation: operation.Name("vlan.create"),
			TargetType: "switch", TargetID: testDeviceID, Status: task.StatusPending,
			ExecutionMode: operation.ExecutionModeSync, Payload: json.RawMessage(`{"vlan_id":100}`),
			CreatedBy: testUserID, CreatedAt: now, Version: 1,
		},
		DryRun: true, IdempotencyKey: "repo-idem-1",
	})
	if err != nil || createdTask.IdempotencyKey != "repo-idem-1" || !createdTask.DryRun {
		t.Fatalf("Create task = %+v, %v", createdTask, err)
	}
	_, err = repos.Tasks.Create(ctx, task.Persisted{
		Task: task.Task{
			ID: "00000000-0000-0000-0000-000000000107", Type: task.TypeOperation,
			Operation: operation.Name("vlan.create"), TargetType: "switch", TargetID: testDeviceID,
			Status: task.StatusPending, ExecutionMode: operation.ExecutionModeSync,
			Payload: json.RawMessage(`{}`), CreatedBy: testUserID,
			CreatedAt: now.Add(time.Second), Version: 1,
		},
		IdempotencyKey: "repo-idem-1",
	})
	if !apperror.IsCode(err, apperror.CodeIdempotencyConflict) {
		t.Fatalf("duplicate idempotency error = %v", err)
	}

	queued := createdTask
	queued.Status = task.StatusQueued
	queued.Version = 2
	queued, err = repos.Tasks.Save(ctx, queued, 1)
	if err != nil || queued.Version != 2 {
		t.Fatalf("Save queued task = %+v, %v", queued, err)
	}
	if _, err := repos.Tasks.Save(ctx, queued, 1); !apperror.IsCode(err, apperror.CodeStateConflict) {
		t.Fatalf("stale task save error = %v", err)
	}
	running, err := repos.Tasks.ClaimNextQueued(ctx, now.Add(2*time.Second))
	if err != nil || running.Status != task.StatusRunning || running.Version != 3 {
		t.Fatalf("ClaimNextQueued() = %+v, %v", running, err)
	}

	auditRecord, err := repos.Audits.Create(ctx, audit.Record{
		ID: testAuditID, RequestID: "req-repo-1", TaskID: testTaskID,
		ActorUserID: testUserID, ActorUsername: "repo-test", ActorRole: "ADMIN",
		SourceIP: "192.0.2.1", Action: "vlan.create", TargetType: "switch",
		TargetID: testDeviceID, DeviceVendor: "HUAWEI", Status: "RUNNING",
		RequestPayloadRedacted: json.RawMessage(`{"vlan_id":100}`), CreatedAt: now,
	})
	if err != nil || auditRecord.SourceIP != "192.0.2.1" {
		t.Fatalf("Create audit = %+v, %v", auditRecord, err)
	}
	completedAudit, err := repos.Audits.Complete(ctx, testAuditID, "SUCCESS", "", json.RawMessage(`{"ok":true}`), now.Add(3*time.Second))
	if err != nil || completedAudit.Status != "SUCCESS" || completedAudit.FinishedAt == nil {
		t.Fatalf("Complete audit = %+v, %v", completedAudit, err)
	}

	count, err := repos.Tasks.InterruptRunning(ctx, now.Add(4*time.Second))
	if err != nil || count != 1 {
		t.Fatalf("InterruptRunning() = %d, %v", count, err)
	}
	interrupted, err := repos.Tasks.Get(ctx, testTaskID)
	if err != nil || interrupted.Status != task.StatusInterrupted || interrupted.Version != 4 {
		t.Fatalf("interrupted task = %+v, %v", interrupted, err)
	}

	if err := repos.Devices.SoftDelete(ctx, testDeviceID); err != nil {
		t.Fatalf("SoftDelete device: %v", err)
	}
	if _, err := repos.Devices.Get(ctx, testDeviceID); !apperror.IsCode(err, apperror.CodeDeviceNotFound) {
		t.Fatalf("deleted device error = %v", err)
	}
}

func runMigration(t *testing.T, root, dsn string, args ...string) {
	t.Helper()
	commandArgs := append([]string{filepath.Join(root, "scripts", "migrate.sh")}, args...)
	cmd := exec.Command("bash", commandArgs...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "DATABASE_URL="+dsn)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("migration %v failed: %v\n%s", args, err, output)
	}
}
