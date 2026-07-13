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
	"github.com/dylanLi233/switch-manager/internal/domain/switchinterface"
	"github.com/dylanLi233/switch-manager/internal/domain/task"
	"github.com/dylanLi233/switch-manager/internal/fakeruntime"
	"github.com/dylanLi233/switch-manager/internal/operationsvc"
	"github.com/dylanLi233/switch-manager/internal/pluginregistry"
	"github.com/dylanLi233/switch-manager/internal/scheduler"
	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
	fakeplugin "github.com/dylanLi233/switch-manager/plugins/fake"
)

const (
	interfaceUserID       = "00000000-0000-0000-0000-000000000601"
	interfaceCredentialID = "00000000-0000-0000-0000-000000000602"
	interfaceDeviceID     = "00000000-0000-0000-0000-000000000603"
)

func TestInterfaceOperationPostgreSQLIntegration(t *testing.T) {
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
	if _, err := store.pool.Exec(ctx, `INSERT INTO users(id, external_subject, username) VALUES ($1::uuid,$2,$3)`, interfaceUserID, "interface-user", "interface-user"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	repositories := store.Repositories()
	if _, err := repositories.Credentials.Create(ctx, credential.Credential{ID: interfaceCredentialID, Name: "interface-credential", Type: credential.TypePassword, Username: "admin", EncryptedSecret: []byte("encrypted"), KeyVersion: "v1", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := repositories.Devices.Create(ctx, device.Device{ID: interfaceDeviceID, Name: "interface-switch", Host: "192.0.2.61", SSHPort: 22, CredentialID: interfaceCredentialID, Vendor: device.VendorHuawei, Model: "FAKE-SW", OSVersion: "fake-1.0", DetectMode: device.DetectModeAuto, IdentityStatus: device.IdentityVerified, Status: device.StatusActive, CreatedAt: now, UpdatedAt: now}); err != nil {
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
	actor := auth.Actor{UserID: interfaceUserID, Username: "interface-user", Role: auth.RoleAdmin, SourceIP: "192.0.2.1"}

	listed := submitInterfaceOperation(t, service, actor, "req-interface-list", pluginapi.OperationInterfaceList, operation.ClassQuery, nil, operation.ExecutionModeSync, false, "")
	var listResult struct {
		Operation struct {
			Data struct {
				Interfaces []switchinterface.Interface `json:"interfaces"`
			} `json:"data"`
		} `json:"operation"`
	}
	if err := json.Unmarshal(listed.Task.Result, &listResult); err != nil || len(listResult.Operation.Data.Interfaces) != 2 {
		t.Fatalf("result=%s decoded=%+v err=%v", listed.Task.Result, listResult, err)
	}

	access := submitInterfaceOperation(t, service, actor, "req-interface-access", pluginapi.OperationInterfaceAccess, operation.ClassConfig, map[string]any{"interface_name": "FakeEthernet1/0/1", "vlan_id": 100}, operation.ExecutionModeSync, false, "")
	if !access.Completed || access.Task.Status != task.StatusSuccess {
		t.Fatalf("access=%+v", access)
	}
	audit, err := repositories.Audits.Get(ctx, access.AuditID)
	if err != nil || audit.Status != string(task.StatusSuccess) {
		t.Fatalf("audit=%+v err=%v", audit, err)
	}

	trunk := submitInterfaceOperation(t, service, actor, "req-interface-trunk", pluginapi.OperationInterfaceTrunk, operation.ClassConfig, map[string]any{"interface_name": "FakeEthernet1/0/2", "allowed_vlans": []int{200, 100}, "native_vlan": 100}, operation.ExecutionModeAsync, false, "")
	waitVLANTaskStatus(t, repositories.Tasks, trunk.Task.ID, task.StatusSuccess)
	state := runtime.SnapshotInterfaces(interfaceDeviceID)
	if state[0].AccessVLAN != 100 || state[1].NativeVLAN != 100 || len(state[1].AllowedVLANs) != 2 {
		t.Fatalf("state=%+v", state)
	}

	submitInterfaceOperation(t, service, actor, "req-interface-add", pluginapi.OperationInterfaceVLANAdd, operation.ClassConfig, map[string]any{"interface_name": "FakeEthernet1/0/2", "vlan_id": 300}, operation.ExecutionModeSync, false, "")
	submitInterfaceOperation(t, service, actor, "req-interface-remove", pluginapi.OperationInterfaceVLANRemove, operation.ClassConfig, map[string]any{"interface_name": "FakeEthernet1/0/2", "vlan_id": 200}, operation.ExecutionModeSync, false, "")
	state = runtime.SnapshotInterfaces(interfaceDeviceID)
	if len(state[1].AllowedVLANs) != 2 || state[1].AllowedVLANs[0] != 100 || state[1].AllowedVLANs[1] != 300 {
		t.Fatalf("membership state=%+v", state[1])
	}

	dry := submitInterfaceOperation(t, service, actor, "req-interface-dry-disable", pluginapi.OperationInterfaceDisable, operation.ClassConfig, map[string]any{"interface_name": "FakeEthernet1/0/1"}, operation.ExecutionModeSync, true, "")
	if !dry.Completed || dry.Task.Status != task.StatusSuccess || runtime.SnapshotInterfaces(interfaceDeviceID)[0].AdminState != switchinterface.AdminEnabled {
		t.Fatalf("dry=%+v state=%+v", dry, runtime.SnapshotInterfaces(interfaceDeviceID))
	}
	submitInterfaceOperation(t, service, actor, "req-interface-disable", pluginapi.OperationInterfaceDisable, operation.ClassConfig, map[string]any{"interface_name": "FakeEthernet1/0/1"}, operation.ExecutionModeSync, false, "")
	if runtime.SnapshotInterfaces(interfaceDeviceID)[0].AdminState != switchinterface.AdminDisabled {
		t.Fatalf("disable state=%+v", runtime.SnapshotInterfaces(interfaceDeviceID))
	}

	var before int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM tasks`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	_, err = service.Submit(ctx, operationsvc.SubmitRequest{RequestID: "req-invalid-interface", Operation: operation.Request{Name: operation.Name(pluginapi.OperationInterfaceGet), Class: operation.ClassQuery, DeviceID: interfaceDeviceID, Parameters: map[string]any{"interface_name": "GigabitEthernet1/0/1"}, ExecutionMode: operation.ExecutionModeSync, Actor: actor}})
	if !apperror.IsCode(err, apperror.CodeValidationError) {
		t.Fatalf("invalid interface error=%v", err)
	}
	var after int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM tasks`).Scan(&after); err != nil || after != before {
		t.Fatalf("task count before=%d after=%d err=%v", before, after, err)
	}

	first := submitInterfaceOperation(t, service, actor, "req-interface-idem-1", pluginapi.OperationInterfaceEnable, operation.ClassConfig, map[string]any{"interface_name": "FakeEthernet1/0/1"}, operation.ExecutionModeAsync, false, "interface-enable-1")
	second := submitInterfaceOperation(t, service, actor, "req-interface-idem-2", pluginapi.OperationInterfaceEnable, operation.ClassConfig, map[string]any{"interface_name": "FakeEthernet1/0/1"}, operation.ExecutionModeAsync, false, "interface-enable-1")
	if !second.IdempotentReplay || second.Task.ID != first.Task.ID {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
	_, err = service.Submit(ctx, operationsvc.SubmitRequest{RequestID: "req-interface-idem-conflict", Operation: operation.Request{Name: operation.Name(pluginapi.OperationInterfaceDisable), Class: operation.ClassConfig, DeviceID: interfaceDeviceID, Parameters: map[string]any{"interface_name": "FakeEthernet1/0/1"}, ExecutionMode: operation.ExecutionModeAsync, IdempotencyKey: "interface-enable-1", Actor: actor}})
	if !apperror.IsCode(err, apperror.CodeIdempotencyConflict) {
		t.Fatalf("idempotency conflict error=%v", err)
	}
}

func submitInterfaceOperation(t *testing.T, service *operationsvc.Service, actor auth.Actor, requestID string, name pluginapi.OperationName, class operation.Class, parameters map[string]any, mode operation.ExecutionMode, dryRun bool, idempotency string) operationsvc.Submission {
	t.Helper()
	submission, err := service.Submit(context.Background(), operationsvc.SubmitRequest{RequestID: requestID, Operation: operation.Request{Name: operation.Name(name), Class: class, DeviceID: interfaceDeviceID, Parameters: parameters, ExecutionMode: mode, DryRun: dryRun, IdempotencyKey: idempotency, Actor: actor}})
	if err != nil {
		t.Fatalf("submit %s: %v", name, err)
	}
	return submission
}
