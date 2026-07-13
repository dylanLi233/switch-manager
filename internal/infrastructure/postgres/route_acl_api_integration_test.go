package postgres

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/dylanLi233/switch-manager/internal/concurrency"
	"github.com/dylanLi233/switch-manager/internal/domain/acl"
	"github.com/dylanLi233/switch-manager/internal/domain/auth"
	"github.com/dylanLi233/switch-manager/internal/domain/credential"
	"github.com/dylanLi233/switch-manager/internal/domain/device"
	"github.com/dylanLi233/switch-manager/internal/domain/operation"
	"github.com/dylanLi233/switch-manager/internal/domain/route"
	"github.com/dylanLi233/switch-manager/internal/domain/task"
	"github.com/dylanLi233/switch-manager/internal/fakeruntime"
	"github.com/dylanLi233/switch-manager/internal/operationsvc"
	"github.com/dylanLi233/switch-manager/internal/pluginregistry"
	"github.com/dylanLi233/switch-manager/internal/scheduler"
	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
	fakeplugin "github.com/dylanLi233/switch-manager/plugins/fake"
)

const (
	routeACLUserID       = "00000000-0000-0000-0000-000000000701"
	routeACLCredentialID = "00000000-0000-0000-0000-000000000702"
	routeACLDeviceID     = "00000000-0000-0000-0000-000000000703"
)

func TestRouteACLOperationPostgreSQLIntegration(t *testing.T) {
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
	if _, err := store.pool.Exec(ctx, `INSERT INTO users(id, external_subject, username) VALUES ($1::uuid,$2,$3)`, routeACLUserID, "route-acl-user", "route-acl-user"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	repositories := store.Repositories()
	if _, err := repositories.Credentials.Create(ctx, credential.Credential{ID: routeACLCredentialID, Name: "route-acl-credential", Type: credential.TypePassword, Username: "admin", EncryptedSecret: []byte("encrypted"), KeyVersion: "v1", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := repositories.Devices.Create(ctx, device.Device{ID: routeACLDeviceID, Name: "route-acl-switch", Host: "192.0.2.71", SSHPort: 22, CredentialID: routeACLCredentialID, Vendor: device.VendorHuawei, Model: "FAKE-SW", OSVersion: "fake-1.0", DetectMode: device.DetectModeAuto, IdentityStatus: device.IdentityVerified, Status: device.StatusActive, CreatedAt: now, UpdatedAt: now}); err != nil {
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
	actor := auth.Actor{UserID: routeACLUserID, Username: "route-acl-user", Role: auth.RoleAdmin, SourceIP: "192.0.2.1"}

	routeSpec := route.Spec{AddressFamily: route.FamilyIPv4, Destination: "192.0.2.0/24", NextHop: "198.51.100.1", OutgoingInterface: "FakeEthernet1/0/1"}
	createdRoute, err := service.Submit(ctx, operationsvc.SubmitRequest{RequestID: "req-route-create", Operation: operation.Request{Name: operation.Name(pluginapi.OperationRouteCreate), Class: operation.ClassConfig, DeviceID: routeACLDeviceID, Parameters: map[string]any{"route": routeSpec}, ExecutionMode: operation.ExecutionModeSync, Actor: actor}})
	if err != nil || !createdRoute.Completed || createdRoute.Task.Status != task.StatusSuccess {
		t.Fatalf("createdRoute=%+v err=%v", createdRoute, err)
	}
	routes := runtime.SnapshotRoutes(routeACLDeviceID)
	if len(routes) != 1 || routes[0].RouteID != "route-000001" {
		t.Fatalf("routes=%+v", routes)
	}
	routeAudit, err := repositories.Audits.Get(ctx, createdRoute.AuditID)
	if err != nil || routeAudit.Status != string(task.StatusSuccess) {
		t.Fatalf("audit=%+v err=%v", routeAudit, err)
	}

	updatedSpec := route.Spec{AddressFamily: route.FamilyIPv4, Destination: "203.0.113.0/24", NextHop: "198.51.100.1"}
	updatedRoute, err := service.Submit(ctx, operationsvc.SubmitRequest{RequestID: "req-route-update", Operation: operation.Request{Name: operation.Name(pluginapi.OperationRouteUpdate), Class: operation.ClassConfig, DeviceID: routeACLDeviceID, Parameters: map[string]any{"route_id": "route-000001", "route": updatedSpec}, ExecutionMode: operation.ExecutionModeAsync, Actor: actor}})
	if err != nil || updatedRoute.Task.Status != task.StatusQueued {
		t.Fatalf("updatedRoute=%+v err=%v", updatedRoute, err)
	}
	waitRouteACLTaskStatus(t, repositories.Tasks, updatedRoute.Task.ID, task.StatusSuccess)
	if runtime.SnapshotRoutes(routeACLDeviceID)[0].Destination != "203.0.113.0/24" {
		t.Fatalf("routes=%+v", runtime.SnapshotRoutes(routeACLDeviceID))
	}

	dryDelete, err := service.Submit(ctx, operationsvc.SubmitRequest{RequestID: "req-route-dry-delete", Operation: operation.Request{Name: operation.Name(pluginapi.OperationRouteDelete), Class: operation.ClassConfig, DeviceID: routeACLDeviceID, Parameters: map[string]any{"route_id": "route-000001"}, ExecutionMode: operation.ExecutionModeSync, DryRun: true, Actor: actor}})
	if err != nil || !dryDelete.Completed || len(runtime.SnapshotRoutes(routeACLDeviceID)) != 1 {
		t.Fatalf("dryDelete=%+v routes=%+v err=%v", dryDelete, runtime.SnapshotRoutes(routeACLDeviceID), err)
	}

	aclSpec := acl.Spec{SchemaVersion: acl.ExperimentalSchemaVersion, Name: "FAKE_ACL_WEB", AddressFamily: acl.FamilyIPv4, Rules: []acl.Rule{{Sequence: 10, Action: acl.ActionPermit, Protocol: acl.ProtocolTCP, Source: "any", Destination: "192.0.2.0/24", DestinationPorts: []acl.PortRange{{From: 443, To: 443}}}}}
	createdACL, err := service.Submit(ctx, operationsvc.SubmitRequest{RequestID: "req-acl-create", Operation: operation.Request{Name: operation.Name(pluginapi.OperationACLCreate), Class: operation.ClassConfig, DeviceID: routeACLDeviceID, Parameters: map[string]any{"acl": aclSpec}, ExecutionMode: operation.ExecutionModeSync, IdempotencyKey: "acl-web-create", Actor: actor}})
	if err != nil || !createdACL.Completed || createdACL.Task.Status != task.StatusSuccess {
		t.Fatalf("createdACL=%+v err=%v", createdACL, err)
	}
	if state := runtime.SnapshotACLs(routeACLDeviceID); len(state) != 1 || state[0].ACLID != "acl-000001" {
		t.Fatalf("acls=%+v", state)
	}
	replayed, err := service.Submit(ctx, operationsvc.SubmitRequest{RequestID: "req-acl-replay", Operation: operation.Request{Name: operation.Name(pluginapi.OperationACLCreate), Class: operation.ClassConfig, DeviceID: routeACLDeviceID, Parameters: map[string]any{"acl": aclSpec}, ExecutionMode: operation.ExecutionModeSync, IdempotencyKey: "acl-web-create", Actor: actor}})
	if err != nil || !replayed.IdempotentReplay || replayed.Task.ID != createdACL.Task.ID {
		t.Fatalf("replayed=%+v err=%v", replayed, err)
	}
	conflictSpec := aclSpec
	conflictSpec.Name = "FAKE_ACL_OTHER"
	if _, err := service.Submit(ctx, operationsvc.SubmitRequest{RequestID: "req-acl-conflict", Operation: operation.Request{Name: operation.Name(pluginapi.OperationACLCreate), Class: operation.ClassConfig, DeviceID: routeACLDeviceID, Parameters: map[string]any{"acl": conflictSpec}, ExecutionMode: operation.ExecutionModeSync, IdempotencyKey: "acl-web-create", Actor: actor}}); !apperror.IsCode(err, apperror.CodeIdempotencyConflict) {
		t.Fatalf("conflict err=%v", err)
	}

	updatedACLSpec := acl.Spec{SchemaVersion: acl.ExperimentalSchemaVersion, Name: "FAKE_ACL_WEB", AddressFamily: acl.FamilyIPv4, Rules: []acl.Rule{{Sequence: 10, Action: acl.ActionDeny, Protocol: acl.ProtocolAny, Source: "any", Destination: "any"}}}
	updatedACL, err := service.Submit(ctx, operationsvc.SubmitRequest{RequestID: "req-acl-update", Operation: operation.Request{Name: operation.Name(pluginapi.OperationACLUpdate), Class: operation.ClassConfig, DeviceID: routeACLDeviceID, Parameters: map[string]any{"acl_id": "acl-000001", "acl": updatedACLSpec}, ExecutionMode: operation.ExecutionModeSync, Actor: actor}})
	if err != nil || !updatedACL.Completed || runtime.SnapshotACLs(routeACLDeviceID)[0].Rules[0].Action != acl.ActionDeny {
		t.Fatalf("updatedACL=%+v state=%+v err=%v", updatedACL, runtime.SnapshotACLs(routeACLDeviceID), err)
	}

	deletedACL, err := service.Submit(ctx, operationsvc.SubmitRequest{RequestID: "req-acl-delete", Operation: operation.Request{Name: operation.Name(pluginapi.OperationACLDelete), Class: operation.ClassConfig, DeviceID: routeACLDeviceID, Parameters: map[string]any{"acl_id": "acl-000001"}, ExecutionMode: operation.ExecutionModeSync, Actor: actor}})
	if err != nil || !deletedACL.Completed || len(runtime.SnapshotACLs(routeACLDeviceID)) != 0 {
		t.Fatalf("deletedACL=%+v state=%+v err=%v", deletedACL, runtime.SnapshotACLs(routeACLDeviceID), err)
	}
	deletedRoute, err := service.Submit(ctx, operationsvc.SubmitRequest{RequestID: "req-route-delete", Operation: operation.Request{Name: operation.Name(pluginapi.OperationRouteDelete), Class: operation.ClassConfig, DeviceID: routeACLDeviceID, Parameters: map[string]any{"route_id": "route-000001"}, ExecutionMode: operation.ExecutionModeSync, Actor: actor}})
	if err != nil || !deletedRoute.Completed || len(runtime.SnapshotRoutes(routeACLDeviceID)) != 0 {
		t.Fatalf("deletedRoute=%+v routes=%+v err=%v", deletedRoute, runtime.SnapshotRoutes(routeACLDeviceID), err)
	}
}

func waitRouteACLTaskStatus(t *testing.T, repository *TaskRepository, id string, want task.Status) task.Persisted {
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
