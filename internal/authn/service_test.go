package authn

import (
	"context"
	"errors"
	"testing"

	"github.com/dylanLi233/switch-manager/internal/domain/auth"
)

type verifierStub struct {
	identity Identity
	err      error
}

func (s verifierStub) Verify(context.Context, string) (Identity, error) { return s.identity, s.err }

type principalRepositoryStub struct {
	principal auth.Principal
	err       error
	subject   string
}

func (s *principalRepositoryStub) ResolveBySubject(_ context.Context, subject string) (auth.Principal, error) {
	s.subject = subject
	return s.principal, s.err
}

func TestServiceResolvesDatabasePrincipal(t *testing.T) {
	t.Parallel()
	repository := &principalRepositoryStub{principal: auth.Principal{
		UserID: "user-1", Subject: "subject-1", Username: "alice",
		Bindings: []auth.Binding{{Role: auth.RoleViewer, Scope: auth.Scope{Type: auth.ScopeGlobal}, Permissions: []auth.Permission{auth.PermissionDeviceRead}}},
	}}
	service, err := NewService(verifierStub{identity: Identity{Subject: "subject-1", ServiceActorID: "ops-web"}}, repository)
	if err != nil {
		t.Fatal(err)
	}
	principal, err := service.Authenticate(context.Background(), "token")
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if repository.subject != "subject-1" || principal.ServiceActorID != "ops-web" {
		t.Fatalf("principal = %+v, subject = %q", principal, repository.subject)
	}
}

func TestServiceStopsWhenVerificationFails(t *testing.T) {
	t.Parallel()
	repository := &principalRepositoryStub{}
	service, err := NewService(verifierStub{err: errors.New("bad token")}, repository)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(context.Background(), "token"); err == nil {
		t.Fatal("expected authentication error")
	}
	if repository.subject != "" {
		t.Fatal("repository must not be called after verification failure")
	}
}
