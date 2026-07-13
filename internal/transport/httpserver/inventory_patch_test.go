package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dylanLi233/switch-manager/internal/domain/auth"
)

func TestEmptyInventoryPatchesAreRejected(t *testing.T) {
	service := &inventoryServiceStub{}
	principal := principal(auth.RoleAdmin, auth.PermissionCredentialManage, auth.PermissionDeviceManage)
	router := inventoryRouter(t, principal, service)
	for _, path := range []string{"/api/v1/credentials/credential-id", "/api/v1/switches/switch-id"} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, authorizedRequest(http.MethodPatch, path, `{}`))
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("path=%s status=%d body=%s", path, recorder.Code, recorder.Body.String())
		}
	}
}
