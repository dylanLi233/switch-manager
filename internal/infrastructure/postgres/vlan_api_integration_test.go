package postgres

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/dylanLi233/switch-manager/internal/concurrency"
	"github.com/dylanLi233/switch-manager/internal/domain/auth"
	"github.com/dylanLi233/switch-manager/internal/domain/credential"
	"github.com/dylanLi233/switch-manager/internal/domain/device"
	"github.com/dylanLi233/switch-manager/internal/domain/operation"
	"github.com/dylanLi233/switch-manager/internal/domain/task"
	"github.com/dylanLi233/switch-manager/internal/fakeruntime"
	"github.com/dylanLi233/switch-manager/internal/operationsvc"
	"github.com/dylanLi233/switch-manager/internal/pluginregistry"
	"github.com/dylanLi233/switch-manager/internal/scheduler"
	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
	fakeplugin "github.com/dylanLi233/switch-manager/plugins/fake"
)

const (
	vlanUserID       = "00000000-0000-0000-0000-000000000501"
	vlanCredentialID = "00000000-0000-0000-0000-000000000502"
	vlanDeviceID     = "00000000-0000-0000-0000-000000000503"
)

func TestVLANOperationPostgreSQLIntegration(t *testing.T) {
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
	if _, err := store.pool.Exec(ctx, `INSERT INTO users(id, external_subject, username) VALUES ($1::uuid,$2,$3)`, vlanUserID, "vlan-user", "vlan-user"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	repositories := store.Repositories()
	if _, err := repositories.Credentials.Create(ctx, credential.Credential{ID: vlanCredentialID, Name: "vlan-credential", Type: credential.TypePassword, Username: "admin", EncryptedSecret: []byte("encrypted"), KeyVersion: "v1", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := repositories.Devices.Create(ctx, device.Device{ID: vlanDeviceID, Name: "vlan-switch", Host: "192.0.2.60", SSHPort: 22, CredentialID: vlanCredentialID, Vendor: device.VendorHuawei, Model: "FAKE-SW", OSVersion: "fake-1.0", DetectMode: device.DetectModeAuto, IdentityStatus: device.IdentityVerified, Status: device.StatusActive, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}

	registry := pluginregistry.NewCurrent()
	plugin, _ := fakeplugin.New(pluginapi.VendorHuawei)
	if err := registry.Register(plugin); err != nil {
		t.Fatal(err)
	}
	planner, err := operationsvc.NewPlanner(repositories.Devices, registry)
	if err != nil {
		t.Fatal(err)
	}
	runtime := fakeruntime.New()
	guards, _ := concurrency.NewController(5)
	executor, err := operationsvc.NewExecutor(planner, repositories.Audits, runtime, guards)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := scheduler.New(repositories.Tasks, executor, scheduler.Config{Workers: 2, PollInterval: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	committer, err := NewOperationSubmission(store)
	if err != nil {
		t.Fatal(err)
	}
	service, err := operationsvc.NewService(repositories.Tasks, repositories.Audits, planner, dispatcher, committer, operationsvc.Config{SyncWaitTimeout: 3 * time.Second, WaitPollInterval: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	runCtx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- dispatcher.Run(runCtx) }()
	defer func() {
		stop()
		if err := <-done; err != nil {
			t.Fatalf("scheduler run: %v", err)
		}
	}()

	actor := auth.Actor{UserID: vlanUserID, Username: "vlan-user", Role: auth.RoleAdmin, SourceIP: "192.0.2.1"}
	create := operation.Request{Name: operation.Name(pluginapi.OperationVLANCreate), Class: operation.ClassConfig, DeviceID: vlanDeviceID, Parameters: map[string]any{"vlan_id": 100, "name": "office"}, ExecutionMode: operation.ExecutionModeSync, Actor: actor}
	created, err := service.Submit(ctx, operationsvc.SubmitRequest{RequestID: "req-vlan-create", Operation: create})
	if err != nil {
		t.Fatal(err)
	}
	if !created.Completed || created.Task.Status != task.StatusSuccess || len(runtime.Snapshot(vlanDeviceID)) != 1 {
		t.Fatalf("created=%+v state=%+v", created, runtime.Snapshot(vlanDeviceID))
	}
	createdAudit, err := repositories.Audits.Get(ctx, created.AuditID)
	if err != nil || createdAudit.Status != string(task.StatusSuccess) {
		t.Fatalf("audit=%+v err=%v", createdAudit, err)
	}

	listed, err := service.Submit(ctx, operationsvc.SubmitRequest{RequestID: "req-vlan-list", Operation: operation.Request{Name: operation.Name(pluginapi.OperationVLANList), Class: operation.ClassQuery, DeviceID: vlanDeviceID, ExecutionMode: operation.ExecutionModeSync, Actor: actor}})
	if err != nil || !listed.Completed || listed.Task.Status != task.StatusSuccess {
		t.Fatalf("listed=%+v err=%v", listed, err)
	}
	var listResult struct {
		Operation struct {
			Data struct {
				VLANs []struct {
					ID int `json:"vlan_id"`
				} `json:"vlans"`
			} `json:"data"`
		} `json:"operation"`
	}
	if err := json.Unmarshal(listed.Task.Result, &listResult); err != nil || len(listResult.Operation.Data.VLANs) != 1 || listResult.Operation.Data.VLANs[0].ID != 100 {
		t.Fatalf("result=%s decoded=%+v err=%v", listed.Task.Result, listResult, err)
	}

	updated, err := service.Submit(ctx, operationsvc.SubmitRequest{RequestID: "req-vlan-update", Operation: operation.Request{Name: operation.Name(pluginapi.OperationVLANUpdate), Class: operation.ClassConfig, DeviceID: vlanDeviceID, Parameters: map[string]any{"vlan_id": 100, "name": "staff"}, ExecutionMode: operation.ExecutionModeAsync, Actor: actor}})
	if err != nil || updated.Task.Status != task.StatusQueued {
		t.Fatalf("updated=%+v err=%v", updated, err)
	}
	waitVLANTaskStatus(t, repositories.Tasks, updated.Task.ID, task.StatusSuccess)
	if state := runtime.Snapshot(vlanDeviceID); len(state) != 1 || state[0].Name != "staff" {
		t.Fatalf("state=%+v", state)
	}

	dryDelete, err := service.Submit(ctx, operationsvc.SubmitRequest{RequestID: "req-vlan-dry-delete", Operation: operation.Request{Name: operation.Name(pluginapi.OperationVLANDelete), Class: operation.ClassConfig, DeviceID: vlanDeviceID, Parameters: map[string]any{"vlan_id": 100}, ExecutionMode: operation.ExecutionModeSync, DryRun: true, Actor: actor}})
	if err != nil || !dryDelete.Completed || dryDelete.Task.Status != task.StatusSuccess || len(runtime.Snapshot(vlanDeviceID)) != 1 {
		t.Fatalf("dryDelete=%+v state=%+v err=%v", dryDelete, runtime.Snapshot(vlanDeviceID), err)
	}
	if !json.Valid(dryDelete.Task.Result) || !containsJSONBoolean(dryDelete.Task.Result, "dry_run", true) {
		t.Fatalf("dry result=%s", dryDelete.Task.Result)
	}

	idempotent := create
	idempotent.Parameters = map[string]any{"vlan_id": 200, "name": "guest"}
	idempotent.ExecutionMode = operation.ExecutionModeAsync
	idempotent.IdempotencyKey = "create-vlan-200"
	first, err := service.Submit(ctx, operationsvc.SubmitRequest{RequestID: "req-idem-1", Operation: idempotent})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Submit(ctx, operationsvc.SubmitRequest{RequestID: "req-idem-2", Operation: idempotent})
	if err != nil || !second.IdempotentReplay || second.Task.ID != first.Task.ID {
		t.Fatalf("first=%+v second=%+v err=%v", first, second, err)
	}
	conflict := idempotent
	conflict.Parameters = map[string]any{"vlan_id": 201, "name": "guest"}
	if _, err := service.Submit(ctx, operationsvc.SubmitRequest{RequestID: "req-idem-conflict", Operation: conflict}); !apperror.IsCode(err, apperror.CodeIdempotencyConflict) {
		t.Fatalf("conflict error=%v", err)
	}

	deleted, err := service.Submit(ctx, operationsvc.SubmitRequest{RequestID: "req-vlan-delete", Operation: operation.Request{Name: operation.Name(pluginapi.OperationVLANDelete), Class: operation.ClassConfig, DeviceID: vlanDeviceID, Parameters: map[string]any{"vlan_id": 100}, ExecutionMode: operation.ExecutionModeSync, Actor: actor}})
	if err != nil || !deleted.Completed || deleted.Task.Status != task.StatusSuccess {
		t.Fatalf("deleted=%+v err=%v", deleted, err)
	}
	for _, value := range runtime.Snapshot(vlanDeviceID) {
		if value.ID == 100 {
			t.Fatalf("VLAN 100 still exists: %+v", runtime.Snapshot(vlanDeviceID))
		}
	}
}

func waitVLANTaskStatus(t *testing.T, repository *TaskRepository, id string, want task.Status) task.Persisted {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		value, err := repository.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if value.Status == want {
			return value
		}
		if value.Status.Terminal() && value.Status != want {
			t.Fatalf("task %s terminal status=%s error=%s", id, value.Status, value.ErrorCode)
		}
		if time.Now().After(deadline) {
			t.Fatalf("task %s status=%s want=%s", id, value.Status, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func containsJSONBoolean(data []byte, key string, want bool) bool {
	var object map[string]any
	if json.Unmarshal(data, &object) != nil {
		return false
	}
	value, ok := object[key].(bool)
	return ok && value == want
}
