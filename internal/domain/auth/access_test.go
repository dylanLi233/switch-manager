package auth

import "testing"

func TestScopeCovers(t *testing.T) {
	t.Parallel()
	global := Scope{Type: ScopeGlobal}
	resource := Scope{Type: ScopeSpecificResource, ID: "switch-1"}
	if !global.Covers(resource) {
		t.Fatal("global scope must cover a specific resource")
	}
	if !(Scope{Type: ScopeSpecificResource, ID: "switch-1"}).Covers(resource) {
		t.Fatal("exact scope must match")
	}
	if (Scope{Type: ScopeSpecificResource, ID: "switch-2"}).Covers(resource) {
		t.Fatal("different resource scope must not match")
	}
	if (Scope{Type: ScopeProject, ID: "project-1"}).Covers(resource) {
		t.Fatal("V1 must not infer scope hierarchy")
	}
}

func TestPermissionValidationIsExtensible(t *testing.T) {
	t.Parallel()
	for _, permission := range []Permission{PermissionDeviceRead, "vm.network.configure", "resource-group.read_2"} {
		if err := permission.Validate(); err != nil {
			t.Fatalf("Validate(%q) error = %v", permission, err)
		}
	}
	for _, permission := range []Permission{"", "device", "Device.Read", "device..read", "device/read", " device.read"} {
		if err := permission.Validate(); err == nil {
			t.Fatalf("Validate(%q) expected error", permission)
		}
	}
}

func TestPrincipalAuthorize(t *testing.T) {
	t.Parallel()
	principal := Principal{
		UserID: "user-1", Subject: "subject-1", Username: "alice",
		Bindings: []Binding{
			{Role: RoleViewer, Scope: Scope{Type: ScopeGlobal}, Permissions: []Permission{PermissionDeviceRead}},
			{Role: RoleAdmin, Scope: Scope{Type: ScopeSpecificResource, ID: "switch-1"}, Permissions: []Permission{PermissionDeviceManage}},
		},
	}
	if role, ok := principal.Authorize(PermissionDeviceRead, Scope{Type: ScopeSpecificResource, ID: "switch-2"}); !ok || role != RoleViewer {
		t.Fatalf("global viewer authorization = %q, %v", role, ok)
	}
	if role, ok := principal.Authorize(PermissionDeviceManage, Scope{Type: ScopeSpecificResource, ID: "switch-1"}); !ok || role != RoleAdmin {
		t.Fatalf("resource admin authorization = %q, %v", role, ok)
	}
	if _, ok := principal.Authorize(PermissionDeviceManage, Scope{Type: ScopeSpecificResource, ID: "switch-2"}); ok {
		t.Fatal("unexpected authorization outside exact scope")
	}
}

func TestPrincipalAuthorizePrefersExactScope(t *testing.T) {
	t.Parallel()
	principal := Principal{
		UserID: "user-1", Subject: "subject-1", Username: "alice",
		Bindings: []Binding{
			{Role: RoleAdmin, Scope: Scope{Type: ScopeGlobal}, Permissions: []Permission{PermissionDeviceRead}},
			{Role: RoleViewer, Scope: Scope{Type: ScopeSpecificResource, ID: "switch-1"}, Permissions: []Permission{PermissionDeviceRead}},
		},
	}
	role, ok := principal.Authorize(PermissionDeviceRead, Scope{Type: ScopeSpecificResource, ID: "switch-1"})
	if !ok || role != RoleViewer {
		t.Fatalf("exact scope role = %q, %v; want VIEWER", role, ok)
	}
}

func TestPrincipalAuthorizeUsesDeterministicRoleRank(t *testing.T) {
	t.Parallel()
	principal := Principal{
		UserID: "user-1", Subject: "subject-1", Username: "alice",
		Bindings: []Binding{
			{Role: RoleViewer, Scope: Scope{Type: ScopeGlobal}, Permissions: []Permission{PermissionDeviceRead}},
			{Role: RoleAdmin, Scope: Scope{Type: ScopeGlobal}, Permissions: []Permission{PermissionDeviceRead}},
		},
	}
	role, ok := principal.Authorize(PermissionDeviceRead, Scope{Type: ScopeProject, ID: "project-1"})
	if !ok || role != RoleAdmin {
		t.Fatalf("effective role = %q, %v; want ADMIN", role, ok)
	}
}

func TestBindingRejectsDuplicatePermission(t *testing.T) {
	t.Parallel()
	binding := Binding{
		Role: RoleAdmin,
		Scope: Scope{Type: ScopeGlobal},
		Permissions: []Permission{PermissionDeviceRead, PermissionDeviceRead},
	}
	if err := binding.Validate(); err == nil {
		t.Fatal("expected duplicate permission error")
	}
}
