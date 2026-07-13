package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/dylanLi233/switch-manager/internal/domain/acl"
	"github.com/dylanLi233/switch-manager/internal/domain/auth"
	"github.com/dylanLi233/switch-manager/internal/domain/operation"
	"github.com/dylanLi233/switch-manager/internal/domain/task"
	"github.com/dylanLi233/switch-manager/internal/health"
	"github.com/dylanLi233/switch-manager/internal/operationsvc"
)

func routeACLRouter(t *testing.T, principal auth.Principal, submitter *operationSubmitterStub) http.Handler {
	t.Helper()
	handlers, err := NewRouteACLHandlers(submitter)
	if err != nil {
		t.Fatal(err)
	}
	return NewAuthenticatedRouter(health.NewHandler(time.Second), 1<<20, staticInventoryAuth{principal: principal}, handlers)
}

func TestViewerCanReadRouteButCannotConfigure(t *testing.T) {
	submitter := &operationSubmitterStub{submission: operationsvc.Submission{Task: task.Persisted{Task: task.Task{ID: "route-list", Status: task.StatusSuccess, Result: json.RawMessage(`{"dry_run":false,"operation":{"data":{"routes":[]}},"audit_completed":true}`)}}, Completed: true}}
	router := routeACLRouter(t, principal(auth.RoleViewer, auth.PermissionOperationQuery), submitter)
	read := httptest.NewRecorder()
	router.ServeHTTP(read, authorizedRequest(http.MethodGet, "/api/v1/switches/device/routes", ""))
	if read.Code != http.StatusOK || submitter.calls != 1 {
		t.Fatalf("read status=%d calls=%d body=%s", read.Code, submitter.calls, read.Body.String())
	}
	write := httptest.NewRecorder()
	router.ServeHTTP(write, authorizedRequest(http.MethodPost, "/api/v1/switches/device/routes", `{"address_family":"IPV4","destination":"192.0.2.0/24","next_hop":"198.51.100.1"}`))
	if write.Code != http.StatusForbidden || submitter.calls != 1 {
		t.Fatalf("write status=%d calls=%d body=%s", write.Code, submitter.calls, write.Body.String())
	}
}

func TestACLIsExplicitlyExperimentalAndTyped(t *testing.T) {
	submitter := &operationSubmitterStub{submission: operationsvc.Submission{Task: task.Persisted{Task: task.Task{ID: "acl-create", Status: task.StatusSuccess, Result: json.RawMessage(`{"dry_run":false,"operation":{"data":{"schema_version":"experimental-v1","acl":{"acl_id":"acl-000001","schema_version":"experimental-v1","name":"FAKE_ACL_WEB","address_family":"IPV4","rules":[{"sequence":10,"action":"PERMIT","protocol":"ANY","source":"any","destination":"any"}]} }},"audit_completed":true}`)}}, Completed: true}}
	router := routeACLRouter(t, principal(auth.RoleAdmin, auth.PermissionOperationConfig), submitter)
	body := `{"schema_version":"experimental-v1","name":"FAKE_ACL_WEB","address_family":"IPV4","rules":[{"sequence":10,"action":"PERMIT","protocol":"ANY","source":"any","destination":"any"}]}`
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, authorizedRequest(http.MethodPost, "/api/v1/switches/device/acls", body))
	if recorder.Code != http.StatusOK || recorder.Header().Get(ExperimentalACLHeader) != acl.ExperimentalSchemaVersion {
		t.Fatalf("status=%d header=%q body=%s", recorder.Code, recorder.Header().Get(ExperimentalACLHeader), recorder.Body.String())
	}
	request := submitter.request.Operation
	if request.Name != "acl.create" || request.Class != operation.ClassConfig {
		t.Fatalf("request=%+v", request)
	}
	encoded, _ := json.Marshal(request.Parameters["acl"])
	if strings.Contains(string(encoded), "metadata") || !strings.Contains(string(encoded), `"schema_version":"experimental-v1"`) {
		t.Fatalf("parameters=%s", encoded)
	}
}

func TestACLUnknownFieldsFailBeforeSubmission(t *testing.T) {
	submitter := &operationSubmitterStub{}
	router := routeACLRouter(t, principal(auth.RoleAdmin, auth.PermissionOperationConfig), submitter)
	body := `{"schema_version":"experimental-v1","name":"FAKE_ACL_WEB","address_family":"IPV4","rules":[{"sequence":10,"action":"PERMIT","protocol":"ANY","source":"any","destination":"any","vendor_cli":"permit ip any any"}]}`
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, authorizedRequest(http.MethodPost, "/api/v1/switches/device/acls", body))
	if recorder.Code != http.StatusBadRequest || submitter.calls != 0 {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, submitter.calls, recorder.Body.String())
	}
}

func TestUnsupportedRouteReturns422(t *testing.T) {
	submitter := &operationSubmitterStub{err: apperror.New(apperror.CodeCapabilityNotSupported, "")}
	router := routeACLRouter(t, principal(auth.RoleViewer, auth.PermissionOperationQuery), submitter)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, authorizedRequest(http.MethodGet, "/api/v1/switches/device/routes", ""))
	if recorder.Code != http.StatusUnprocessableEntity || !strings.Contains(recorder.Body.String(), `"code":"CAPABILITY_NOT_SUPPORTED"`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestRouteNormalizesBeforeSubmission(t *testing.T) {
	submitter := &operationSubmitterStub{submission: operationsvc.Submission{Task: task.Persisted{Task: task.Task{ID: "route-create", Status: task.StatusQueued}}}}
	router := routeACLRouter(t, principal(auth.RoleAdmin, auth.PermissionOperationConfig), submitter)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, authorizedRequest(http.MethodPost, "/api/v1/switches/device/routes", `{"address_family":"IPV4","destination":"192.0.2.7/24","next_hop":"198.51.100.1","execution_mode":"ASYNC"}`))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	routeValue, _ := json.Marshal(submitter.request.Operation.Parameters["route"])
	if !strings.Contains(string(routeValue), `"destination":"192.0.2.0/24"`) {
		t.Fatalf("route=%s", routeValue)
	}
}
