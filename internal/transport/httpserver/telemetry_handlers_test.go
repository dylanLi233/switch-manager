package httpserver

import (
	"encoding/json"
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

func telemetryRouter(t *testing.T, principal auth.Principal, submitter *operationSubmitterStub, resultLimit int) http.Handler {
	t.Helper()
	handlers, err := NewTelemetryHandlers(submitter, resultLimit)
	if err != nil {
		t.Fatal(err)
	}
	return NewAuthenticatedRouter(health.NewHandler(time.Second), 1<<20, staticInventoryAuth{principal: principal}, handlers)
}

func TestViewerCanQueryTelemetryWithServerLimit(t *testing.T) {
	submitter := &operationSubmitterStub{submission: operationsvc.Submission{Task: task.Persisted{Task: task.Task{ID: "mac-task", Status: task.StatusSuccess, Result: json.RawMessage(`{"dry_run":false,"operation":{"data":{"entries":[],"page":2,"page_size":20,"total":0}},"audit_completed":true}`)}}, Completed: true}}
	router := telemetryRouter(t, principal(auth.RoleViewer, auth.PermissionOperationQuery), submitter, 1234)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, authorizedRequest(http.MethodGet, "/api/v1/switches/device/mac-table?page=2&page_size=20", ""))
	if recorder.Code != http.StatusOK || submitter.calls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, submitter.calls, recorder.Body.String())
	}
	parameters := submitter.request.Operation.Parameters
	if parameters["page"] != 2 || parameters["page_size"] != 20 || parameters["result_limit"] != 1234 {
		t.Fatalf("parameters=%v", parameters)
	}
}

func TestTelemetryRejectsClientResultLimitAndInvalidPagination(t *testing.T) {
	for _, path := range []string{
		"/api/v1/switches/device/mac-table?result_limit=999999",
		"/api/v1/switches/device/arp-table?page=0",
		"/api/v1/switches/device/arp-table?page_size=501",
		"/api/v1/switches/device/status?page=1",
	} {
		submitter := &operationSubmitterStub{}
		router := telemetryRouter(t, principal(auth.RoleViewer, auth.PermissionOperationQuery), submitter, 5000)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, authorizedRequest(http.MethodGet, path, ""))
		if recorder.Code != http.StatusBadRequest || submitter.calls != 0 {
			t.Fatalf("path=%s status=%d calls=%d body=%s", path, recorder.Code, submitter.calls, recorder.Body.String())
		}
	}
}

func TestTelemetryResultTooLargeMapsTo422(t *testing.T) {
	submitter := &operationSubmitterStub{submission: operationsvc.Submission{Task: task.Persisted{Task: task.Task{ID: "large-task", Status: task.StatusFailed, ErrorCode: string(apperror.CodeResultTooLarge)}}, Completed: true}}
	router := telemetryRouter(t, principal(auth.RoleViewer, auth.PermissionOperationQuery), submitter, 1)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, authorizedRequest(http.MethodGet, "/api/v1/switches/device/mac-table", ""))
	if recorder.Code != http.StatusUnprocessableEntity || !strings.Contains(recorder.Body.String(), `"code":"RESULT_TOO_LARGE"`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestTelemetryRequiresQueryPermission(t *testing.T) {
	submitter := &operationSubmitterStub{}
	router := telemetryRouter(t, principal(auth.RoleAdmin, auth.PermissionOperationConfig), submitter, 5000)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, authorizedRequest(http.MethodGet, "/api/v1/switches/device/status", ""))
	if recorder.Code != http.StatusForbidden || submitter.calls != 0 {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, submitter.calls, recorder.Body.String())
	}
}
