package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dylanLi233/switch-manager/internal/domain/auth"
	"github.com/dylanLi233/switch-manager/internal/domain/operation"
	"github.com/dylanLi233/switch-manager/internal/domain/task"
	"github.com/dylanLi233/switch-manager/internal/health"
	"github.com/dylanLi233/switch-manager/internal/operationsvc"
	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
)

func interfaceRouter(t *testing.T, principal auth.Principal, submitter *operationSubmitterStub) http.Handler {
	t.Helper()
	handlers, err := NewInterfaceHandlers(submitter)
	if err != nil {
		t.Fatal(err)
	}
	return NewAuthenticatedRouter(health.NewHandler(time.Second), 1<<20, staticInventoryAuth{principal: principal}, handlers)
}

func TestViewerCanQueryButCannotConfigureInterface(t *testing.T) {
	submitter := &operationSubmitterStub{submission: operationsvc.Submission{Task: task.Persisted{Task: task.Task{ID: "query", Status: task.StatusSuccess, Result: json.RawMessage(`{"dry_run":false,"operation":{"data":{"interfaces":[]}},"audit_completed":true}`)}}, Completed: true}}
	router := interfaceRouter(t, principal(auth.RoleViewer, auth.PermissionOperationQuery), submitter)
	query := httptest.NewRecorder()
	router.ServeHTTP(query, authorizedRequest(http.MethodGet, "/api/v1/switches/device/interfaces", ""))
	if query.Code != http.StatusOK || submitter.calls != 1 {
		t.Fatalf("query status=%d calls=%d body=%s", query.Code, submitter.calls, query.Body.String())
	}
	write := httptest.NewRecorder()
	router.ServeHTTP(write, authorizedRequest(http.MethodPost, "/api/v1/switches/device/interfaces/FakeEthernet1%2F0%2F1:disable", `{}`))
	if write.Code != http.StatusForbidden || submitter.calls != 1 {
		t.Fatalf("write status=%d calls=%d body=%s", write.Code, submitter.calls, write.Body.String())
	}
}

func TestTrunkAsyncMapsSortedVLANsAndOptions(t *testing.T) {
	submitter := &operationSubmitterStub{submission: operationsvc.Submission{Task: task.Persisted{Task: task.Task{ID: "task-interface", Status: task.StatusQueued}}}}
	router := interfaceRouter(t, principal(auth.RoleAdmin, auth.PermissionOperationConfig), submitter)
	request := authorizedRequest(http.MethodPut, "/api/v1/switches/device/interfaces/FakeEthernet1%2F0%2F2/trunk", `{"allowed_vlans":[200,100],"native_vlan":100,"execution_mode":"ASYNC","save_config":true}`)
	request.Header.Set("Idempotency-Key", "interface-trunk-1")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	got := submitter.request.Operation
	if got.Name != operation.Name(pluginapi.OperationInterfaceTrunk) || got.ExecutionMode != operation.ExecutionModeAsync || !got.SaveConfig || got.IdempotencyKey != "interface-trunk-1" {
		t.Fatalf("request=%+v", got)
	}
	vlans := got.Parameters["allowed_vlans"].([]int)
	if got.Parameters["interface_name"] != "FakeEthernet1/0/2" || len(vlans) != 2 || vlans[0] != 100 || vlans[1] != 200 {
		t.Fatalf("parameters=%v", got.Parameters)
	}
}

func TestInterfaceGenericValidationRunsBeforeSubmission(t *testing.T) {
	for _, test := range []struct{ method, path, body string }{
		{http.MethodPut, "/api/v1/switches/device/interfaces/FakeEthernet1%2F0%2F2/trunk", `{"allowed_vlans":[],"native_vlan":100}`},
		{http.MethodPut, "/api/v1/switches/device/interfaces/FakeEthernet1%2F0%2F2/trunk", `{"allowed_vlans":[100],"native_vlan":200}`},
		{http.MethodPut, "/api/v1/switches/device/interfaces/FakeEthernet1%2F0%2F1/access", `{"vlan_id":4095}`},
		{http.MethodPost, "/api/v1/switches/device/interfaces/FakeEthernet1%2F0%2F1%0Ashutdown:enable", `{}`},
	} {
		submitter := &operationSubmitterStub{}
		router := interfaceRouter(t, principal(auth.RoleAdmin, auth.PermissionOperationConfig), submitter)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, authorizedRequest(test.method, test.path, test.body))
		if recorder.Code != http.StatusBadRequest || submitter.calls != 0 {
			t.Fatalf("%s %s status=%d calls=%d body=%s", test.method, test.path, recorder.Code, submitter.calls, recorder.Body.String())
		}
	}
}
