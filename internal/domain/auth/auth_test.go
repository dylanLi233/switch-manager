package auth

import "testing"

func TestRoleValidate(t *testing.T) {
	t.Parallel()
	for _, role := range []Role{RoleViewer, RoleAdmin, RoleAuditor} {
		if err := role.Validate(); err != nil {
			t.Fatalf("Validate(%q) error = %v", role, err)
		}
	}
	if err := Role("ROOT").Validate(); err == nil {
		t.Fatal("expected unsupported role error")
	}
}

func TestActorValidate(t *testing.T) {
	t.Parallel()
	valid := Actor{UserID: "user-1", Username: "alice", Role: RoleAdmin}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	tests := []Actor{
		{Username: "alice", Role: RoleAdmin},
		{UserID: "user-1", Role: RoleAdmin},
		{UserID: "user-1", Username: "alice", Role: Role("ROOT")},
	}
	for _, actor := range tests {
		if err := actor.Validate(); err == nil {
			t.Fatalf("Validate(%+v) expected error", actor)
		}
	}
}
