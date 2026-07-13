package postgres

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
	batchUserID       = "00000000-0000-0000-0000-000000000701"
	batchCredentialID = "00000000-0000-0000-0000-000000000702"
	batchDevice1      = "00000000-0000-0000-0000-000000000711"
	batchDevice2      = "00000000-0000-0000-0000-000000000712"
	batchDevice3      = "00000000-0000-0000-0000-000000000713"
)

type selectiveBatchFactory struct {
	base *fakeruntime.Factory
	mu   sync.RWMutex
	fail map[string]bool
}

func (f *selectiveBatchFactory) setFailures(ids ...string) {
	f.mu.Lock()
	f.fail = make(map[string]bool, len(ids))
	for _, id := range ids {
		f.fail[id] = true
	}
	f.mu.Unlock()
}

func (f *selectiveBatchFactory) Open(ctx context.Context, managed device.Device) (operationsvc.Session, error) {
	session, err := f.base.Open(ctx, managed)
	if err != nil {
		return nil, err
	}
	f.mu.RLock()
	fail := f.fail[managed.ID]
	f.mu.RUnlock()
	if !fail {
		return session, nil
	}
	return &failingBatchSession{Session: session}, nil
}

type failingBatchSession struct{ operationsvc.Session }

func (s *failingBatchSession) Execute(context.Context, pluginapi.PlannedCommand) (pluginapi.CommandOutput, error) {
	return pluginapi.CommandOutput{}, apperror.New(apperror.CodeCommandRejected, "")
}

func TestBatchOperationPostgreSQLIntegration(t *testing.T) {
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
	if _, err := store.pool.Exec(ctx, `INSERT INTO users(id,external_subject,username) VALUES ($1::uuid,$2,$3)`, batchUserID, "batch-user", "batch-user"); err != nil {
		t.Fatal(err)
	}
	repos := store.Repositories()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := repos.Credentials.Create(ctx, credential.Credential{ID: batchCredentialID, Name: "batch-credential", Type: credential.TypePassword, Username: "admin", EncryptedSecret: []byte("encrypted"), KeyVersion: "v1", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	for index, id := range []string{batchDevice1, batchDevice2, batchDevice3} {
		if _, err := repos.Devices.Create(ctx, device.Device{ID: id, Name: "batch-switch", Host: "192.0.2." + string(rune('1'+index)), SSHPort: 22, CredentialID: batchCredentialID, Vendor: device.VendorHuawei, Model: "FAKE-SW", OSVersion: "1.0", DetectMode: device.DetectModeAuto, IdentityStatus: device.IdentityVerified, Status: device.StatusActive, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}

	registry := pluginregistry.NewCurrent()
	plugin, _ := fakeplugin.New(pluginapi.VendorHuawei)
	if err := registry.Register(plugin); err != nil {
		t.Fatal(err)
	}
	planner, err := operationsvc.NewPlanner(repos.Devices, registry)
	if err != nil {
		t.Fatal(err)
	}
	batchStore, err := NewBatchStore(store)
	if err != nil {
		t.Fatal(err)
	}
	batchTasks, err := operationsvc.NewBatchAwareTaskRepository(repos.Tasks, batchStore)
	if err != nil {
		t.Fatal(err)
	}
	factory := &selectiveBatchFactory{base: fakeruntime.New(), fail: map[string]bool{}}
	guards, _ := concurrency.NewController(5)
	executor, err := operationsvc.NewExecutor(planner, repos.Audits, factory, guards)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := scheduler.New(batchTasks, executor, scheduler.Config{Workers: 1, PollInterval: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	service, err := operationsvc.NewBatchService(batchStore, planner, dispatcher, operationsvc.Config{SyncWaitTimeout: 3 * time.Second, WaitPollInterval: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	runCtx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- dispatcher.Run(runCtx) }()
	defer func() {
		stop()
		if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("scheduler run: %v", err)
		}
	}()
	actor := auth.Actor{UserID: batchUserID, Username: "batch-user", Role: auth.RoleAdmin, SourceIP: "192.0.2.200"}
	targets := []string{batchDevice3, batchDevice1, batchDevice2}

	t.Run("all success", func(t *testing.T) {
		factory.setFailures()
		snapshot, err := service.Submit(ctx, operationsvc.BatchSubmitRequest{RequestID: "batch-success", TargetSwitchIDs: targets, Operation: operation.Name(pluginapi.OperationDeviceStatusGet), ContinueOnFailure: true, Actor: actor})
		if err != nil {
			t.Fatal(err)
		}
		finished := waitBatchTerminal(t, service, snapshot.Batch.ID)
		if finished.Batch.Status != task.StatusSuccess || finished.Batch.SuccessCount != 3 || finished.Batch.FailedCount != 0 || finished.Batch.CancelledCount != 0 {
			t.Fatalf("batch=%+v", finished.Batch)
		}
		for index, item := range finished.Items {
			if item.Item.SequenceNo != index+1 || item.Child.Status != task.StatusSuccess {
				t.Fatalf("item=%+v", item)
			}
			if index > 0 && finished.Items[index-1].Item.DeviceID > item.Item.DeviceID {
				t.Fatalf("items are not sorted by device ID: %+v", finished.Items)
			}
		}
	})

	var partial task.BatchSnapshot
	t.Run("mixed result does not roll back success", func(t *testing.T) {
		factory.setFailures(batchDevice2)
		created, err := service.Submit(ctx, operationsvc.BatchSubmitRequest{RequestID: "batch-partial", TargetSwitchIDs: targets, Operation: operation.Name(fakeplugin.OperationEchoQuery), Parameters: map[string]any{"message": "hello"}, ContinueOnFailure: true, Actor: actor})
		if err != nil {
			t.Fatal(err)
		}
		partial = waitBatchTerminal(t, service, created.Batch.ID)
		if partial.Batch.Status != task.StatusPartialSuccess || partial.Batch.SuccessCount != 2 || partial.Batch.FailedCount != 1 || partial.Batch.CancelledCount != 0 {
			t.Fatalf("batch=%+v", partial.Batch)
		}
		for _, item := range partial.Items {
			if item.Item.DeviceID != batchDevice2 && item.Child.Status != task.StatusSuccess {
				t.Fatalf("successful child was changed: %+v", item)
			}
		}
	})

	t.Run("retry failed copies only failed child", func(t *testing.T) {
		factory.setFailures()
		retry, err := service.RetryFailed(ctx, partial.Batch.ID, "batch-retry", actor)
		if err != nil {
			t.Fatal(err)
		}
		finished := waitBatchTerminal(t, service, retry.Batch.ID)
		if finished.Batch.TotalCount != 1 || finished.Batch.Status != task.StatusSuccess || finished.Parent.RetryOf != partial.Parent.ID {
			t.Fatalf("retry=%+v", finished)
		}
		if finished.Items[0].Item.DeviceID != batchDevice2 || finished.Items[0].Child.RetryOf == "" {
			t.Fatalf("retry item=%+v", finished.Items[0])
		}
	})

	t.Run("all fail", func(t *testing.T) {
		factory.setFailures(batchDevice1, batchDevice2, batchDevice3)
		created, err := service.Submit(ctx, operationsvc.BatchSubmitRequest{RequestID: "batch-failed", TargetSwitchIDs: targets, Operation: operation.Name(fakeplugin.OperationEchoQuery), Parameters: map[string]any{"message": "fail"}, ContinueOnFailure: true, Actor: actor})
		if err != nil {
			t.Fatal(err)
		}
		finished := waitBatchTerminal(t, service, created.Batch.ID)
		if finished.Batch.Status != task.StatusFailed || finished.Batch.FailedCount != 3 {
			t.Fatalf("batch=%+v", finished.Batch)
		}
	})

	t.Run("stop on failure cancels unstarted children", func(t *testing.T) {
		factory.setFailures(batchDevice1, batchDevice2, batchDevice3)
		created, err := service.Submit(ctx, operationsvc.BatchSubmitRequest{RequestID: "batch-stop", TargetSwitchIDs: targets, Operation: operation.Name(fakeplugin.OperationEchoQuery), Parameters: map[string]any{"message": "stop"}, ContinueOnFailure: false, Actor: actor})
		if err != nil {
			t.Fatal(err)
		}
		finished := waitBatchTerminal(t, service, created.Batch.ID)
		if finished.Batch.Status != task.StatusPartialSuccess || finished.Batch.FailedCount != 1 || finished.Batch.CancelledCount != 2 {
			t.Fatalf("batch=%+v items=%+v", finished.Batch, finished.Items)
		}
	})
}

func waitBatchTerminal(t *testing.T, service *operationsvc.BatchService, id string) task.BatchSnapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		value, err := service.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if value.Batch.Status.Terminal() {
			return value
		}
		if time.Now().After(deadline) {
			t.Fatalf("batch %s did not finish: %+v", id, value.Batch)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
