package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/dylanLi233/switch-manager/internal/domain/acl"
	"github.com/dylanLi233/switch-manager/internal/domain/auth"
)

func TestACLExperimentalHeaderCoversUnauthorizedForbiddenAndUnsupported(t *testing.T) {
	tests := []struct {
		name      string
		principal auth.Principal
		submitter *operationSubmitterStub
		request   *http.Request
		status    int
	}{
		{
			name: "unauthorized", principal: principal(auth.RoleViewer, auth.PermissionOperationQuery),
			submitter: &operationSubmitterStub{}, request: httptest.NewRequest(http.MethodGet, "/api/v1/switches/device/acls", nil), status: http.StatusUnauthorized,
		},
		{
			name: "forbidden", principal: principal(auth.RoleViewer, auth.PermissionOperationQuery),
			submitter: &operationSubmitterStub{}, request: authorizedRequest(http.MethodPost, "/api/v1/switches/device/acls", `{"schema_version":"experimental-v1","name":"FAKE_ACL_WEB","address_family":"IPV4","rules":[{"sequence":10,"action":"PERMIT","protocol":"ANY","source":"any","destination":"any"}]}`), status: http.StatusForbidden,
		},
		{
			name: "unsupported", principal: principal(auth.RoleViewer, auth.PermissionOperationQuery),
			submitter: &operationSubmitterStub{err: apperror.New(apperror.CodeCapabilityNotSupported, "")}, request: authorizedRequest(http.MethodGet, "/api/v1/switches/device/acls", ""), status: http.StatusUnprocessableEntity,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := routeACLRouter(t, test.principal, test.submitter)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, test.request)
			if recorder.Code != test.status {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if recorder.Header().Get(ExperimentalACLHeader) != acl.ExperimentalSchemaVersion {
				t.Fatalf("header=%q", recorder.Header().Get(ExperimentalACLHeader))
			}
		})
	}
}
