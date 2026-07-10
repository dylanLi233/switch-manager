package postgres

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/dylanLi233/switch-manager/internal/domain/auth"
)

func TestAccessRepositoryIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("TEST_DATABASE_DSN is not set")
	}
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	runMigration(t, root, dsn, "down", "all")
	runMigration(t, root, dsn, "up")

	ctx := context.Background()
	store, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Repositories().Access.CheckReady(ctx); err != nil {
		t.Fatalf("CheckReady() error = %v", err)
	}

	const activeUserID = "00000000-0000-0000-0000-000000000201"
	const disabledUserID = "00000000-0000-0000-0000-000000000202"
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO users(id, external_subject, username, status) VALUES
		($1::uuid, 'rbac-active', 'alice', 'ACTIVE'),
		($2::uuid, 'rbac-disabled', 'disabled', 'DISABLED')`, activeUserID, disabledUserID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO user_role_bindings(user_id, role_id, scope_type, scope_id)
		SELECT $1::uuid, id, 'GLOBAL', '' FROM roles WHERE name='VIEWER'`, activeUserID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO user_role_bindings(user_id, role_id, scope_type, scope_id)
		SELECT $1::uuid, id, 'SPECIFIC_RESOURCE', 'switch-1' FROM roles WHERE name='ADMIN'`, activeUserID); err != nil {
		t.Fatal(err)
	}

	principal, err := store.Repositories().Access.ResolveBySubject(ctx, "rbac-active")
	if err != nil {
		t.Fatalf("ResolveBySubject() error = %v", err)
	}
	if principal.UserID != activeUserID || principal.Username != "alice" {
		t.Fatalf("principal = %+v", principal)
	}
	if role, ok := principal.Authorize(auth.PermissionDeviceRead, auth.Scope{Type: auth.ScopeSpecificResource, ID: "switch-2"}); !ok || role != auth.RoleViewer {
		t.Fatalf("global read authorization = %q, %v", role, ok)
	}
	if role, ok := principal.Authorize(auth.PermissionDeviceManage, auth.Scope{Type: auth.ScopeSpecificResource, ID: "switch-1"}); !ok || role != auth.RoleAdmin {
		t.Fatalf("resource manage authorization = %q, %v", role, ok)
	}
	if _, ok := principal.Authorize(auth.PermissionDeviceManage, auth.Scope{Type: auth.ScopeSpecificResource, ID: "switch-2"}); ok {
		t.Fatal("unexpected manage permission outside bound resource")
	}

	for _, subject := range []string{"rbac-disabled", "rbac-missing"} {
		if _, err := store.Repositories().Access.ResolveBySubject(ctx, subject); !apperror.IsCode(err, apperror.CodeForbidden) {
			t.Fatalf("subject %q error = %v", subject, err)
		}
	}
}
