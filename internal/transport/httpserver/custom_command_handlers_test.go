package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dylanLi233/switch-manager/internal/domain/auth"
	"github.com/dylanLi233/switch-manager/internal/domain/operation"
	"github.com/dylanLi233/switch-manager/internal/domain/task"
	"github.com/dylanLi233/switch-manager/internal/operationsvc"
	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
)

func customCommandRouter(t *testing.T, principal auth.Principal, submitter *operationSubmitterStub) http.Handler {
	t.Helper()
	return telemetryRouter(t, principal, submitter, 5000)
}

func successfulCommandSubmission() operationsvc.Submission {
	return operationsvc.Submission{
		Task: task.Persisted{Task: task.Task{
			ID: "command-task", Status: task.StatusSuccess,
			Result: json.RawMessage(`{"dry_run":false,"operation":{"data":{"outputs":["ok"]}},"audit_completed":true}`),
		}},
		Completed: true,
	}
}

func TestViewerCanExecuteReadonlyCustomCommand(t *testing.T) {
	submitter := &operationSubmitterStub{submission: successfulCommandSubmission()}
	router := customCommandRouter(t, principal(auth.RoleViewer, auth.PermissionOperationCustomRead), submitter)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, authorizedRequest(http.MethodPost, "/api/v1/switches/device/commands:readonly", `{"commands":["fake.show status"]}`))
	if recorder.Code != http.StatusOK || submitter.calls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, submitter.calls, recorder.Body.String())
	}
	request := submitter.request.Operation
	if request.Name != operation.Name(pluginapi.OperationCommandExecuteReadonly) || request.Class != operation.ClassQuery || request.ExecutionMode != operation.ExecutionModeSync {
		t.Fatalf("request=%+v", request)
	}
	commands, ok := request.Parameters["commands"].([]string)
	if !ok || len(commands) != 1 || commands[0] != "fake.show status" {
		t.Fatalf("parameters=%v", request.Parameters)
	}
}

func TestViewerCannotExecuteConfigCustomCommand(t *testing.T) {
	submitter := &operationSubmitterStub{}
	router := customCommandRouter(t, principal(auth.RoleViewer, auth.PermissionOperationCustomRead), submitter)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, authorizedRequest(http.MethodPost, "/api/v1/switches/device/commands:config", `{"commands":["fake.set description"]}`))
	if recorder.Code != http.StatusForbidden || submitter.calls != 0 {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, submitter.calls, recorder.Body.String())
	}
}

func TestAdminConfigCustomCommandMapsOptions(t *testing.T) {
	submitter := &operationSubmitterStub{submission: operationsvc.Submission{Task: task.Persisted{Task: task.Task{ID: "async-task", Status: task.StatusQueued}}, Completed: false}}
	router := customCommandRouter(t, principal(auth.RoleAdmin, auth.PermissionOperationCustomConfig), submitter)
	recorder := httptest.NewRecorder()
	request := authorizedRequest(http.MethodPost, "/api/v1/switches/device/commands:config", `{"commands":["fake.set high-risk value"],"execution_mode":"ASYNC","dry_run":true,"save_config":true,"confirm_risk":true}`)
	request.Header.Set("Idempotency-Key", "custom-command-1")
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted || submitter.calls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, submitter.calls, recorder.Body.String())
	}
	operationRequest := submitter.request.Operation
	if operationRequest.Name != operation.Name(pluginapi.OperationCommandExecuteConfig) || operationRequest.Class != operation.ClassConfig || operationRequest.ExecutionMode != operation.ExecutionModeAsync || !operationRequest.DryRun || !operationRequest.SaveConfig || !operationRequest.ConfirmRisk || operationRequest.IdempotencyKey != "custom-command-1" {
		t.Fatalf("request=%+v", operationRequest)
	}
}

func TestReadonlyRejectsSaveConfigAndUnknownJSON(t *testing.T) {
	for _, body := range []string{
		`{"commands":["fake.show status"],"save_config":true}`,
		`{"commands":["fake.show status"],"unknown":true}`,
	} {
		submitter := &operationSubmitterStub{}
		router := customCommandRouter(t, principal(auth.RoleViewer, auth.PermissionOperationCustomRead), submitter)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, authorizedRequest(http.MethodPost, "/api/v1/switches/device/commands:readonly", body))
		if recorder.Code != http.StatusBadRequest || submitter.calls != 0 {
			t.Fatalf("body=%s status=%d calls=%d response=%s", body, recorder.Code, submitter.calls, recorder.Body.String())
		}
	}
}
