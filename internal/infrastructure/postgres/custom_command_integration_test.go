package postgres

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	customCommandUserID       = "00000000-0000-0000-0000-000000001901"
	customCommandCredentialID = "00000000-0000-0000-0000-000000001902"
	customCommandDeviceID     = "00000000-0000-0000-0000-000000001903"
)

type countingRuntime struct {
	inner *fakeruntime.Factory
	mu    sync.Mutex
	opens int
}

func (f *countingRuntime) Open(ctx context.Context, value device.Device) (operationsvc.Session, error) {
	f.mu.Lock()
	f.opens++
	f.mu.Unlock()
	return f.inner.Open(ctx, value)
}

func (f *countingRuntime) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.opens
}

func TestCustomCommandSecurityPostgreSQLIntegration(t *testing.T) {
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
	if _, err := store.pool.Exec(ctx, `INSERT INTO users(id,external_subject,username) VALUES ($1::uuid,$2,$3)`, customCommandUserID, "custom-command-user", "custom-command-user"); err != nil {
		t.Fatal(err)
	}
	repositories := store.Repositories()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := repositories.Credentials.Create(ctx, credential.Credential{ID: customCommandCredentialID, Name: "custom-command-credential", Type: credential.TypePassword, Username: "admin", EncryptedSecret: []byte("encrypted"), KeyVersion: "v1", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := repositories.Devices.Create(ctx, device.Device{ID: customCommandDeviceID, Name: "custom-command-switch", Host: "192.0.2.190", SSHPort: 22, CredentialID: customCommandCredentialID, Vendor: device.VendorHuawei, Model: "FAKE-SW", OSVersion: "fake-1.0", DetectMode: device.DetectModeAuto, IdentityStatus: device.IdentityVerified, Status: device.StatusActive, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}

	registry := pluginregistry.NewCurrent()
	plugin, err := fakeplugin.New(pluginapi.VendorHuawei)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(plugin); err != nil {
		t.Fatal(err)
	}
	planner, err := operationsvc.NewPlanner(repositories.Devices, registry)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &countingRuntime{inner: fakeruntime.New()}
	guards, err := concurrency.NewController(5)
	if err != nil {
		t.Fatal(err)
	}
	executor, err := operationsvc.NewExecutor(planner, repositories.Audits, runtime, guards)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := scheduler.New(repositories.Tasks, executor, scheduler.Config{Workers: 1, PollInterval: 5 * time.Millisecond})
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
			t.Fatalf("scheduler: %v", err)
		}
	}()
	actor := auth.Actor{UserID: customCommandUserID, Username: "custom-command-user", Role: auth.RoleAdmin, SourceIP: "192.0.2.1"}

	secretSubmission, err := service.Submit(ctx, operationsvc.SubmitRequest{RequestID: "req-custom-secret", Operation: operation.Request{Name: operation.Name(pluginapi.OperationCommandExecuteReadonly), Class: operation.ClassQuery, DeviceID: customCommandDeviceID, Parameters: map[string]any{"commands": []string{"fake.show secret"}}, ExecutionMode: operation.ExecutionModeSync, Actor: actor}})
	if err != nil {
		t.Fatal(err)
	}
	if !secretSubmission.Completed || secretSubmission.Task.Status != task.StatusSuccess {
		t.Fatalf("secret task=%+v", secretSubmission.Task)
	}
	resultText := string(secretSubmission.Task.Result)
	if strings.Contains(resultText, "secret-token") || !strings.Contains(resultText, "[REDACTED]") {
		t.Fatalf("secret leaked or redaction missing: %s", resultText)
	}
	secretAudit, err := repositories.Audits.Get(ctx, secretSubmission.AuditID)
	if err != nil || strings.Contains(string(secretAudit.ResultSummaryRedacted), "secret-token") {
		t.Fatalf("secret audit=%+v err=%v", secretAudit, err)
	}

	largeSubmission, err := service.Submit(ctx, operationsvc.SubmitRequest{RequestID: "req-custom-large", Operation: operation.Request{Name: operation.Name(pluginapi.OperationCommandExecuteReadonly), Class: operation.ClassQuery, DeviceID: customCommandDeviceID, Parameters: map[string]any{"commands": []string{"fake.show large"}}, ExecutionMode: operation.ExecutionModeSync, Actor: actor}})
	if err != nil {
		t.Fatal(err)
	}
	if largeSubmission.Task.Status != task.StatusFailed || largeSubmission.Task.ErrorCode != string(apperror.CodeResultTooLarge) {
		t.Fatalf("large task=%+v", largeSubmission.Task)
	}

	beforeDryRun := runtime.count()
	_, err = service.Submit(ctx, operationsvc.SubmitRequest{RequestID: "req-custom-high-unconfirmed", Operation: operation.Request{Name: operation.Name(pluginapi.OperationCommandExecuteConfig), Class: operation.ClassConfig, DeviceID: customCommandDeviceID, Parameters: map[string]any{"commands": []string{"fake.set high-risk description"}}, ExecutionMode: operation.ExecutionModeSync, DryRun: true, Actor: actor}})
	if !apperror.IsCode(err, apperror.CodeRiskConfirmationRequired) {
		t.Fatalf("unconfirmed high-risk error=%v", err)
	}
	confirmedDryRun, err := service.Submit(ctx, operationsvc.SubmitRequest{RequestID: "req-custom-high-confirmed", Operation: operation.Request{Name: operation.Name(pluginapi.OperationCommandExecuteConfig), Class: operation.ClassConfig, DeviceID: customCommandDeviceID, Parameters: map[string]any{"commands": []string{"fake.set high-risk description"}}, ExecutionMode: operation.ExecutionModeSync, DryRun: true, ConfirmRisk: true, Actor: actor}})
	if err != nil || confirmedDryRun.Task.Status != task.StatusSuccess || runtime.count() != beforeDryRun {
		t.Fatalf("confirmed dry-run task=%+v opens=%d err=%v", confirmedDryRun.Task, runtime.count(), err)
	}
	var dryResult map[string]any
	if err := json.Unmarshal(confirmedDryRun.Task.Result, &dryResult); err != nil || dryResult["dry_run"] != true {
		t.Fatalf("dry result=%s err=%v", confirmedDryRun.Task.Result, err)
	}

	_, err = service.Submit(ctx, operationsvc.SubmitRequest{RequestID: "req-custom-blocked", Operation: operation.Request{Name: operation.Name(pluginapi.OperationCommandExecuteConfig), Class: operation.ClassConfig, DeviceID: customCommandDeviceID, Parameters: map[string]any{"commands": []string{"fake.set blocked erase"}}, ExecutionMode: operation.ExecutionModeSync, ConfirmRisk: true, Actor: actor}})
	if !apperror.IsCode(err, apperror.CodeDangerousCommandBlocked) {
		t.Fatalf("blocked error=%v", err)
	}
}
