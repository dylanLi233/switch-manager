package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dylanLi233/switch-manager/internal/domain/auth"
	"github.com/dylanLi233/switch-manager/internal/domain/device"
	"github.com/dylanLi233/switch-manager/internal/health"
	"github.com/dylanLi233/switch-manager/internal/inventorysvc"
)

type staticInventoryAuth struct{ principal auth.Principal }

func (a staticInventoryAuth) Authenticate(context.Context, string) (auth.Principal, error) {
	return a.principal, nil
}

type inventoryServiceStub struct {
	createCredentialInput inventorysvc.CredentialInput
	createDeviceCalls     int
	listDeviceCalls       int
}

func (s *inventoryServiceStub) CreateCredential(_ context.Context, input inventorysvc.CredentialInput) (inventorysvc.CredentialView, error) {
	s.createCredentialInput = input
	return inventorysvc.CredentialView{ID: "credential-id", Name: input.Name, Type: input.Type, Username: input.Username, KeyVersion: "v1"}, nil
}
func (*inventoryServiceStub) GetCredential(context.Context, string) (inventorysvc.CredentialView, error) {
	return inventorysvc.CredentialView{}, nil
}
func (*inventoryServiceStub) ListCredentials(context.Context, int, int) ([]inventorysvc.CredentialView, error) {
	return nil, nil
}
func (*inventoryServiceStub) UpdateCredential(context.Context, string, inventorysvc.CredentialPatch) (inventorysvc.CredentialView, error) {
	return inventorysvc.CredentialView{}, nil
}
func (*inventoryServiceStub) DeleteCredential(context.Context, string) error { return nil }
func (s *inventoryServiceStub) CreateDevice(context.Context, inventorysvc.DeviceInput) (inventorysvc.DeviceView, error) {
	s.createDeviceCalls++
	return inventorysvc.DeviceView{}, nil
}
func (*inventoryServiceStub) GetDevice(context.Context, string) (inventorysvc.DeviceView, error) {
	return inventorysvc.DeviceView{}, nil
}
func (s *inventoryServiceStub) ListDevices(context.Context, device.ListFilter) ([]inventorysvc.DeviceView, error) {
	s.listDeviceCalls++
	return []inventorysvc.DeviceView{}, nil
}
func (*inventoryServiceStub) UpdateDevice(context.Context, string, inventorysvc.DevicePatch) (inventorysvc.DeviceView, error) {
	return inventorysvc.DeviceView{}, nil
}
func (*inventoryServiceStub) DeleteDevice(context.Context, string) error { return nil }
func (*inventoryServiceStub) TestConnection(context.Context, string) (inventorysvc.ConnectionTestResult, error) {
	return inventorysvc.ConnectionTestResult{}, nil
}
func (*inventoryServiceStub) Detect(context.Context, string) (inventorysvc.DetectionView, error) {
	return inventorysvc.DetectionView{}, nil
}

func principal(role auth.Role, permissions ...auth.Permission) auth.Principal {
	return auth.Principal{UserID: "user-id", Subject: "subject", Username: "alice", Bindings: []auth.Binding{{Role: role, Scope: auth.Scope{Type: auth.ScopeGlobal}, Permissions: permissions}}}
}

func inventoryRouter(t *testing.T, principal auth.Principal, service *inventoryServiceStub) http.Handler {
	t.Helper()
	handlers, err := NewInventoryHandlers(service)
	if err != nil {
		t.Fatal(err)
	}
	healthHandler := health.NewHandler(time.Second)
	return NewAuthenticatedRouter(healthHandler, 1<<20, staticInventoryAuth{principal: principal}, handlers)
}

func authorizedRequest(method, path, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer token")
	request.Header.Set("Content-Type", "application/json")
	return request
}

func TestCredentialCreateNeverEchoesSecret(t *testing.T) {
	service := &inventoryServiceStub{}
	router := inventoryRouter(t, principal(auth.RoleAdmin, auth.PermissionCredentialManage), service)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, authorizedRequest(http.MethodPost, "/api/v1/credentials", `{"name":"ops","type":"PASSWORD","username":"admin","password":"super-secret"}`))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if service.createCredentialInput.Password != "super-secret" {
		t.Fatalf("input=%+v", service.createCredentialInput)
	}
	lower := strings.ToLower(recorder.Body.String())
	for _, secret := range []string{"super-secret", "password", "private_key", "passphrase"} {
		if strings.Contains(lower, secret) {
			t.Fatalf("response leaked %q: %s", secret, recorder.Body.String())
		}
	}
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["request_id"] == "" || response["success"] != true {
		t.Fatalf("response=%v", response)
	}
}

func TestViewerWriteOperationReturnsForbiddenBeforeService(t *testing.T) {
	service := &inventoryServiceStub{}
	router := inventoryRouter(t, principal(auth.RoleViewer, auth.PermissionDeviceRead), service)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, authorizedRequest(http.MethodPost, "/api/v1/switches", `{"name":"sw","host":"192.0.2.1","credential_id":"cred","vendor":"HUAWEI"}`))
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if service.createDeviceCalls != 0 {
		t.Fatalf("create calls=%d", service.createDeviceCalls)
	}
	if !bytes.Contains(recorder.Body.Bytes(), []byte(`"code":"FORBIDDEN"`)) {
		t.Fatalf("body=%s", recorder.Body.String())
	}
}

func TestViewerCanListDevices(t *testing.T) {
	service := &inventoryServiceStub{}
	router := inventoryRouter(t, principal(auth.RoleViewer, auth.PermissionDeviceRead), service)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, authorizedRequest(http.MethodGet, "/api/v1/switches?page=1&page_size=20&keyword=core", ``))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if service.listDeviceCalls != 1 {
		t.Fatalf("list calls=%d", service.listDeviceCalls)
	}
}

func TestInventoryRoutesRequireAuthentication(t *testing.T) {
	service := &inventoryServiceStub{}
	router := inventoryRouter(t, principal(auth.RoleAdmin, auth.PermissionDeviceManage), service)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/switches", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
