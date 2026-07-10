package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dylanLi233/switch-manager/internal/domain/auth"
)

type authenticatorStub struct {
	principal auth.Principal
	err       error
	token     string
}

func (s *authenticatorStub) Authenticate(_ context.Context, token string) (auth.Principal, error) {
	s.token = token
	return s.principal, s.err
}

func testPrincipal() auth.Principal {
	return auth.Principal{
		UserID: "user-1", Subject: "subject-1", Username: "alice", ServiceActorID: "ops-web",
		Bindings: []auth.Binding{
			{Role: auth.RoleViewer, Scope: auth.Scope{Type: auth.ScopeGlobal}, Permissions: []auth.Permission{auth.PermissionDeviceRead}},
			{Role: auth.RoleAdmin, Scope: auth.Scope{Type: auth.ScopeSpecificResource, ID: "switch-1"}, Permissions: []auth.Permission{auth.PermissionDeviceManage}},
		},
	}
}

func TestAuthenticationMiddlewareRejectsInvalidHeader(t *testing.T) {
	t.Parallel()
	for _, values := range [][]string{nil, {"Basic abc"}, {"Bearer"}, {"Bearer a b"}, {"Bearer one", "Bearer two"}} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/protected", nil)
		for _, value := range values {
			request.Header.Add("Authorization", value)
		}
		handler := withRequestID(AuthenticationMiddleware(&authenticatorStub{principal: testPrincipal()})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Fatal("protected handler must not run")
		})))
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("headers %v status = %d", values, recorder.Code)
		}
	}
}

func TestAuthenticatedRouterMe(t *testing.T) {
	t.Parallel()
	stub := &authenticatorStub{principal: testPrincipal()}
	router := NewAuthenticatedRouter(newHealthyHandler(), 1024, stub)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	request.Header.Set("Authorization", "Bearer signed-token")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || stub.token != "signed-token" {
		t.Fatalf("status=%d token=%q body=%s", recorder.Code, stub.token, recorder.Body.String())
	}
	var response struct {
		Success bool `json:"success"`
		Data struct {
			UserID string `json:"user_id"`
			Roles  []auth.Role `json:"roles"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Success || response.Data.UserID != "user-1" || len(response.Data.Roles) != 2 {
		t.Fatalf("response = %+v", response)
	}
}

func TestRequirePermissionBuildsAuditActor(t *testing.T) {
	t.Parallel()
	stub := &authenticatorStub{principal: testPrincipal()}
	resolver := func(*http.Request) (auth.Scope, error) {
		return auth.Scope{Type: auth.ScopeSpecificResource, ID: "switch-1"}, nil
	}
	var actor auth.Actor
	protected := AuthenticationMiddleware(stub)(RequirePermission(auth.PermissionDeviceManage, resolver)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		actor, err = ActorFromRequest(r)
		if err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusNoContent)
	})))
	request := httptest.NewRequest(http.MethodPost, "/protected", nil)
	request.RemoteAddr = "192.0.2.50:12345"
	request.Header.Set("Authorization", "Bearer token")
	recorder := httptest.NewRecorder()
	withRequestID(protected).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if actor.Role != auth.RoleAdmin || actor.SourceIP != "192.0.2.50" || actor.ServiceActorID != "ops-web" {
		t.Fatalf("actor = %+v", actor)
	}
}

func TestRequirePermissionDeniesOutsideScope(t *testing.T) {
	t.Parallel()
	stub := &authenticatorStub{principal: testPrincipal()}
	resolver := func(*http.Request) (auth.Scope, error) {
		return auth.Scope{Type: auth.ScopeSpecificResource, ID: "switch-2"}, nil
	}
	protected := AuthenticationMiddleware(stub)(RequirePermission(auth.PermissionDeviceManage, resolver)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler must not run")
	})))
	request := httptest.NewRequest(http.MethodPost, "/protected", nil)
	request.Header.Set("Authorization", "Bearer token")
	recorder := httptest.NewRecorder()
	withRequestID(protected).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
