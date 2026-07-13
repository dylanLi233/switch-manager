package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/dylanLi233/switch-manager/internal/domain/auth"
	"github.com/dylanLi233/switch-manager/internal/domain/task"
	"github.com/dylanLi233/switch-manager/internal/health"
	"github.com/dylanLi233/switch-manager/internal/operationsvc"
)

type batchServiceStub struct {
	submits int
	request operationsvc.BatchSubmitRequest
}

func (s *batchServiceStub) Submit(_ context.Context, request operationsvc.BatchSubmitRequest) (task.BatchSnapshot, error) {
	s.submits++
	s.request = request
	if err := request.Validate(); err != nil {
		return task.BatchSnapshot{}, apperror.Wrap(apperror.CodeValidationError, "", err)
	}
	return task.BatchSnapshot{Batch: task.Batch{ID: "batch-id", ParentTaskID: "batch-id", Operation: request.Operation, TotalCount: len(request.TargetSwitchIDs), Status: task.StatusQueued, ContinueOnFailure: request.ContinueOnFailure, CreatedBy: request.Actor.UserID, CreatedAt: time.Now(), UpdatedAt: time.Now()}, Parent: task.Persisted{Task: task.Task{ID: "batch-id", Type: task.TypeBatchParent, Operation: request.Operation, TargetType: "batch", TargetID: "batch-id", Status: task.StatusRunning, ExecutionMode: "ASYNC", Payload: []byte(`{"version":1}`), CreatedBy: request.Actor.UserID, CreatedAt: time.Now(), StartedAt: timePointerForBatchTest(), Version: 1}}}, nil
}
func (*batchServiceStub) Get(context.Context, string) (task.BatchSnapshot, error) { return task.BatchSnapshot{}, nil }
func (*batchServiceStub) Cancel(context.Context, string) (task.BatchSnapshot, error) { return task.BatchSnapshot{}, nil }
func (*batchServiceStub) RetryFailed(context.Context, string, string, auth.Actor) (task.BatchSnapshot, error) { return task.BatchSnapshot{}, nil }

func timePointerForBatchTest() *time.Time { value := time.Now(); return &value }

func batchRouter(t *testing.T, principal auth.Principal, service *batchServiceStub) http.Handler {
	t.Helper()
	handlers, err := NewBatchHandlers(service)
	if err != nil {
		t.Fatal(err)
	}
	return NewAuthenticatedRouter(health.NewHandler(time.Second), 1<<20, staticInventoryAuth{principal: principal}, handlers)
}

func TestBatchQueryUsesLeastPrivilegedEffectiveRole(t *testing.T) {
	service := &batchServiceStub{}
	principal := auth.Principal{UserID: "user-id", Subject: "subject", Username: "alice", Bindings: []auth.Binding{
		{Role: auth.RoleViewer, Scope: auth.Scope{Type: auth.ScopeSpecificResource, ID: "device-1"}, Permissions: []auth.Permission{auth.PermissionOperationQuery}},
		{Role: auth.RoleAdmin, Scope: auth.Scope{Type: auth.ScopeGlobal}, Permissions: []auth.Permission{auth.PermissionOperationQuery}},
	}}
	router := batchRouter(t, principal, service)
	recorder := httptest.NewRecorder()
	body := `{"target_switch_ids":["device-1","device-2"],"operation":"device_status.get"}`
	router.ServeHTTP(recorder, authorizedRequest(http.MethodPost, "/api/v1/batch-operations", body))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if service.submits != 1 || service.request.Actor.Role != auth.RoleViewer {
		t.Fatalf("submits=%d actor=%+v", service.submits, service.request.Actor)
	}
}

func TestBatchConfigRequiresEveryTargetPermission(t *testing.T) {
	service := &batchServiceStub{}
	principal := auth.Principal{UserID: "user-id", Subject: "subject", Username: "alice", Bindings: []auth.Binding{
		{Role: auth.RoleAdmin, Scope: auth.Scope{Type: auth.ScopeSpecificResource, ID: "device-1"}, Permissions: []auth.Permission{auth.PermissionOperationConfig}},
	}}
	router := batchRouter(t, principal, service)
	recorder := httptest.NewRecorder()
	body := `{"target_switch_ids":["device-1","device-2"],"operation":"vlan.create","parameters":{"vlan_id":100}}`
	router.ServeHTTP(recorder, authorizedRequest(http.MethodPost, "/api/v1/batch-operations", body))
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if service.submits != 0 {
		t.Fatalf("submit calls=%d", service.submits)
	}
}

func TestBatchCreateRejectsDuplicateTargetsAndUnknownFields(t *testing.T) {
	principal := auth.Principal{UserID: "user-id", Subject: "subject", Username: "alice", Bindings: []auth.Binding{{Role: auth.RoleAdmin, Scope: auth.Scope{Type: auth.ScopeGlobal}, Permissions: []auth.Permission{auth.PermissionOperationQuery}}}}
	for _, body := range []string{
		`{"target_switch_ids":["device-1","device-1"],"operation":"device_status.get"}`,
		`{"target_switch_ids":["device-1"],"operation":"device_status.get","unexpected":true}`,
	} {
		service := &batchServiceStub{}
		router := batchRouter(t, principal, service)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, authorizedRequest(http.MethodPost, "/api/v1/batch-operations", body))
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d response=%s", body, recorder.Code, recorder.Body.String())
		}
		if !strings.Contains(recorder.Body.String(), `"code":"VALIDATION_ERROR"`) {
			t.Fatalf("response=%s", recorder.Body.String())
		}
	}
}
