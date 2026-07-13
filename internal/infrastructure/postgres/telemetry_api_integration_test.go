package postgres

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dylanLi233/switch-manager/internal/concurrency"
	"github.com/dylanLi233/switch-manager/internal/domain/auth"
	"github.com/dylanLi233/switch-manager/internal/domain/credential"
	"github.com/dylanLi233/switch-manager/internal/domain/device"
	"github.com/dylanLi233/switch-manager/internal/domain/operation"
	"github.com/dylanLi233/switch-manager/internal/domain/task"
	"github.com/dylanLi233/switch-manager/internal/domain/telemetry"
	"github.com/dylanLi233/switch-manager/internal/fakeruntime"
	"github.com/dylanLi233/switch-manager/internal/operationsvc"
	"github.com/dylanLi233/switch-manager/internal/pluginregistry"
	"github.com/dylanLi233/switch-manager/internal/scheduler"
	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
	fakeplugin "github.com/dylanLi233/switch-manager/plugins/fake"
)

const (
	telemetryUserID       = "00000000-0000-0000-0000-000000000801"
	telemetryCredentialID = "00000000-0000-0000-0000-000000000802"
	telemetryDeviceID     = "00000000-0000-0000-0000-000000000803"
)

func TestTelemetryOperationPostgreSQLIntegration(t *testing.T) {
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
	if _, err := store.pool.Exec(ctx, `INSERT INTO users(id, external_subject, username) VALUES ($1::uuid,$2,$3)`, telemetryUserID, "telemetry-user", "telemetry-user"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	repositories := store.Repositories()
	if _, err := repositories.Credentials.Create(ctx, credential.Credential{ID: telemetryCredentialID, Name: "telemetry-credential", Type: credential.TypePassword, Username: "admin", EncryptedSecret: []byte("encrypted"), KeyVersion: "v1", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := repositories.Devices.Create(ctx, device.Device{ID: telemetryDeviceID, Name: "telemetry-switch", Host: "192.0.2.81", SSHPort: 22, CredentialID: telemetryCredentialID, Vendor: device.VendorHuawei, Model: "FAKE-SW", OSVersion: "fake-1.0", DetectMode: device.DetectModeAuto, IdentityStatus: device.IdentityVerified, Status: device.StatusActive, CreatedAt: now, UpdatedAt: now}); err != nil {
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

	actor := auth.Actor{UserID: telemetryUserID, Username: "telemetry-user", Role: auth.RoleViewer, SourceIP: "192.0.2.1"}
	macSubmission, err := service.Submit(ctx, operationsvc.SubmitRequest{RequestID: "req-mac-page", Operation: operation.Request{Name: operation.Name(pluginapi.OperationMACTableList), Class: operation.ClassQuery, DeviceID: telemetryDeviceID, Parameters: map[string]any{"page": 2, "page_size": 1, "result_limit": 5000}, ExecutionMode: operation.ExecutionModeSync, Actor: actor}})
	if err != nil || !macSubmission.Completed || macSubmission.Task.Status != task.StatusSuccess {
		t.Fatalf("MAC submission=%+v err=%v", macSubmission, err)
	}
	var macResult struct {
		Operation struct {
			Data telemetry.MACPage `json:"data"`
		} `json:"operation"`
	}
	if err := json.Unmarshal(macSubmission.Task.Result, &macResult); err != nil {
		t.Fatal(err)
	}
	if macResult.Operation.Data.Total != 2 || macResult.Operation.Data.Page != 2 || len(macResult.Operation.Data.Entries) != 1 {
		t.Fatalf("MAC result=%s", macSubmission.Task.Result)
	}
	macAudit, err := repositories.Audits.Get(ctx, macSubmission.AuditID)
	if err != nil || macAudit.Status != string(task.StatusSuccess) {
		t.Fatalf("MAC audit=%+v err=%v", macAudit, err)
	}

	arpSubmission, err := service.Submit(ctx, operationsvc.SubmitRequest{RequestID: "req-arp-page", Operation: operation.Request{Name: operation.Name(pluginapi.OperationARPTableList), Class: operation.ClassQuery, DeviceID: telemetryDeviceID, Parameters: map[string]any{"page": 1, "page_size": 50, "result_limit": 5000}, ExecutionMode: operation.ExecutionModeSync, Actor: actor}})
	if err != nil || arpSubmission.Task.Status != task.StatusSuccess {
		t.Fatalf("ARP submission=%+v err=%v", arpSubmission, err)
	}

	statusSubmission, err := service.Submit(ctx, operationsvc.SubmitRequest{RequestID: "req-device-status", Operation: operation.Request{Name: operation.Name(pluginapi.OperationDeviceStatusGet), Class: operation.ClassQuery, DeviceID: telemetryDeviceID, ExecutionMode: operation.ExecutionModeSync, Actor: actor}})
	if err != nil || statusSubmission.Task.Status != task.StatusSuccess {
		t.Fatalf("status submission=%+v err=%v", statusSubmission, err)
	}
	var statusResult struct {
		Operation struct {
			Data telemetry.DeviceStatus `json:"data"`
		} `json:"operation"`
	}
	if err := json.Unmarshal(statusSubmission.Task.Result, &statusResult); err != nil || statusResult.Operation.Data.Hostname != "fake-switch" || statusResult.Operation.Data.CollectedAt.IsZero() {
		t.Fatalf("status result=%s decoded=%+v err=%v", statusSubmission.Task.Result, statusResult, err)
	}

	large := []telemetry.MACEntry{
		{MACAddress: "00:11:22:33:44:01", VLANID: 1, InterfaceName: "FakeEthernet1/0/1", EntryType: telemetry.MACDynamic, AgeSeconds: 1},
		{MACAddress: "00:11:22:33:44:02", VLANID: 1, InterfaceName: "FakeEthernet1/0/1", EntryType: telemetry.MACDynamic, AgeSeconds: 2},
		{MACAddress: "00:11:22:33:44:03", VLANID: 1, InterfaceName: "FakeEthernet1/0/2", EntryType: telemetry.MACDynamic, AgeSeconds: 3},
	}
	if err := runtime.ReplaceMACEntries(telemetryDeviceID, large); err != nil {
		t.Fatal(err)
	}
	largeSubmission, err := service.Submit(ctx, operationsvc.SubmitRequest{RequestID: "req-mac-too-large", Operation: operation.Request{Name: operation.Name(pluginapi.OperationMACTableList), Class: operation.ClassQuery, DeviceID: telemetryDeviceID, Parameters: map[string]any{"page": 1, "page_size": 1, "result_limit": 2}, ExecutionMode: operation.ExecutionModeSync, Actor: actor}})
	if err != nil {
		t.Fatal(err)
	}
	if largeSubmission.Task.Status != task.StatusFailed || largeSubmission.Task.ErrorCode != string(pluginapi.ErrorResultTooLarge) {
		t.Fatalf("large task=%+v", largeSubmission.Task)
	}
	largeAudit, err := repositories.Audits.Get(ctx, largeSubmission.AuditID)
	if err != nil || largeAudit.Status != string(task.StatusFailed) || largeAudit.ErrorCode != string(pluginapi.ErrorResultTooLarge) {
		t.Fatalf("large audit=%+v err=%v", largeAudit, err)
	}
}
