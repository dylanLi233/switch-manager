package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/dylanLi233/switch-manager/internal/domain/auth"
	"github.com/dylanLi233/switch-manager/internal/domain/operation"
	"github.com/dylanLi233/switch-manager/internal/domain/task"
	"github.com/dylanLi233/switch-manager/internal/health"
	"github.com/dylanLi233/switch-manager/internal/operationsvc"
)

type operationSubmitterStub struct {
	request    operationsvc.SubmitRequest
	submission operationsvc.Submission
	err        error
	calls      int
}

func (s *operationSubmitterStub) Submit(_ context.Context, request operationsvc.SubmitRequest) (operationsvc.Submission, error) {
	s.calls++
	s.request = request
	return s.submission, s.err
}

func vlanRouter(t *testing.T, principal auth.Principal, submitter *operationSubmitterStub) http.Handler {
	t.Helper()
	handlers, err := NewVLANHandlers(submitter)
	if err != nil {
		t.Fatal(err)
	}
	return NewAuthenticatedRouter(health.NewHandler(time.Second), 1<<20, staticInventoryAuth{principal: principal}, handlers)
}

func TestViewerCanQueryButCannotConfigureVLAN(t *testing.T) {
	submitter := &operationSubmitterStub{submission: operationsvc.Submission{Task: task.Persisted{Task: task.Task{ID: "query-task", Status: task.StatusSuccess, Result: json.RawMessage(`{"dry_run":false,"operation":{"data":{"vlans":[]}},"audit_completed":true}`)}}, Completed: true}}
	router := vlanRouter(t, principal(auth.RoleViewer, auth.PermissionOperationQuery), submitter)
	query := httptest.NewRecorder()
	router.ServeHTTP(query, authorizedRequest(http.MethodGet, "/api/v1/switches/device/vlans", ""))
	if query.Code != http.StatusOK || submitter.calls != 1 {
		t.Fatalf("query status=%d calls=%d body=%s", query.Code, submitter.calls, query.Body.String())
	}
	write := httptest.NewRecorder()
	router.ServeHTTP(write, authorizedRequest(http.MethodPost, "/api/v1/switches/device/vlans", `{"vlan_id":100,"name":"office"}`))
	if write.Code != http.StatusForbidden || submitter.calls != 1 {
		t.Fatalf("write status=%d calls=%d body=%s", write.Code, submitter.calls, write.Body.String())
	}
}

func TestCreateVLANAsyncPassesOptionsAndIdempotency(t *testing.T) {
	submitter := &operationSubmitterStub{submission: operationsvc.Submission{Task: task.Persisted{Task: task.Task{ID: "task-1", Status: task.StatusQueued}}}}
	router := vlanRouter(t, principal(auth.RoleAdmin, auth.PermissionOperationConfig), submitter)
	request := authorizedRequest(http.MethodPost, "/api/v1/switches/device/vlans", `{"vlan_id":100,"name":"office","execution_mode":"ASYNC","dry_run":false,"save_config":true}`)
	request.Header.Set("Idempotency-Key", "vlan-create-100")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	operationRequest := submitter.request.Operation
	if operationRequest.ExecutionMode != operation.ExecutionModeAsync || !operationRequest.SaveConfig || operationRequest.IdempotencyKey != "vlan-create-100" {
		t.Fatalf("request=%+v", operationRequest)
	}
	if operationRequest.Parameters["vlan_id"] != 100 || operationRequest.Parameters["name"] != "office" {
		t.Fatalf("parameters=%v", operationRequest.Parameters)
	}
}

func TestDryRunReturnsPlan(t *testing.T) {
	submitter := &operationSubmitterStub{submission: operationsvc.Submission{Task: task.Persisted{Task: task.Task{ID: "task-dry", Status: task.StatusSuccess, PluginName: "fake-huawei", PluginVersion: "1.1.0", Result: json.RawMessage(`{"dry_run":true,"plan":{"main":{"commands":[{"command":"fake.vlan.create"}]}},"audit_completed":true}`)}, DryRun: true}, Completed: true}}
	router := vlanRouter(t, principal(auth.RoleAdmin, auth.PermissionOperationConfig), submitter)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, authorizedRequest(http.MethodPost, "/api/v1/switches/device/vlans", `{"vlan_id":100,"name":"office","dry_run":true}`))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"plan"`) || !strings.Contains(recorder.Body.String(), `"dry_run":true`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestVLANValidationRunsBeforeSubmission(t *testing.T) {
	for _, test := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, "/api/v1/switches/device/vlans", `{"vlan_id":0,"name":"office"}`},
		{http.MethodPost, "/api/v1/switches/device/vlans", `{"vlan_id":100,"name":"bad\nname"}`},
		{http.MethodPatch, "/api/v1/switches/device/vlans/4095", `{"name":"office"}`},
		{http.MethodPatch, "/api/v1/switches/device/vlans/100", `{}`},
	} {
		submitter := &operationSubmitterStub{}
		router := vlanRouter(t, principal(auth.RoleAdmin, auth.PermissionOperationConfig), submitter)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, authorizedRequest(test.method, test.path, test.body))
		if recorder.Code != http.StatusBadRequest || submitter.calls != 0 {
			t.Fatalf("%s %s status=%d calls=%d body=%s", test.method, test.path, recorder.Code, submitter.calls, recorder.Body.String())
		}
	}
}

func TestSyncTaskFailureMapsToHTTPError(t *testing.T) {
	submitter := &operationSubmitterStub{submission: operationsvc.Submission{Task: task.Persisted{Task: task.Task{ID: "task-failed", Status: task.StatusFailed, ErrorCode: string(apperror.CodeResourceNotFound)}}, Completed: true}}
	router := vlanRouter(t, principal(auth.RoleViewer, auth.PermissionOperationQuery), submitter)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, authorizedRequest(http.MethodGet, "/api/v1/switches/device/vlans/100", ""))
	if recorder.Code != http.StatusNotFound || !strings.Contains(recorder.Body.String(), `"code":"RESOURCE_NOT_FOUND"`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
