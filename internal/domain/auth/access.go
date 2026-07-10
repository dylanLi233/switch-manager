package auth

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Permission is a stable action granted by RBAC.
type Permission string

const (
	PermissionDeviceRead            Permission = "device.read"
	PermissionDeviceManage          Permission = "device.manage"
	PermissionCredentialManage      Permission = "credential.manage"
	PermissionOperationQuery        Permission = "operation.query"
	PermissionOperationConfig       Permission = "operation.config"
	PermissionOperationCustomRead   Permission = "operation.custom_read"
	PermissionOperationCustomConfig Permission = "operation.custom_config"
	PermissionConfigBackup          Permission = "config.backup"
	PermissionConfigRestore         Permission = "config.restore"
	PermissionTaskRead              Permission = "task.read"
	PermissionTaskCancel            Permission = "task.cancel"
	PermissionAuditRead             Permission = "audit.read"
	PermissionAuditExport           Permission = "audit.export"
	PermissionPluginManage          Permission = "plugin.manage"
)

var knownPermissions = map[Permission]struct{}{
	PermissionDeviceRead: {}, PermissionDeviceManage: {}, PermissionCredentialManage: {},
	PermissionOperationQuery: {}, PermissionOperationConfig: {}, PermissionOperationCustomRead: {},
	PermissionOperationCustomConfig: {}, PermissionConfigBackup: {}, PermissionConfigRestore: {},
	PermissionTaskRead: {}, PermissionTaskCancel: {}, PermissionAuditRead: {},
	PermissionAuditExport: {}, PermissionPluginManage: {},
}

// Validate reports whether the permission is part of the V1 permission catalog.
func (p Permission) Validate() error {
	if _, ok := knownPermissions[p]; !ok {
		return fmt.Errorf("unsupported permission %q", p)
	}
	return nil
}

// ScopeType identifies the boundary where a role binding applies.
type ScopeType string

const (
	ScopeGlobal           ScopeType = "GLOBAL"
	ScopeEnvironment      ScopeType = "ENVIRONMENT"
	ScopeProject          ScopeType = "PROJECT"
	ScopeResourceGroup    ScopeType = "RESOURCE_GROUP"
	ScopeSwitchGroup      ScopeType = "SWITCH_GROUP"
	ScopeSpecificResource ScopeType = "SPECIFIC_RESOURCE"
)

// Scope is an exact authorization target. GLOBAL uses an empty ID.
type Scope struct {
	Type ScopeType
	ID   string
}

// Validate enforces the same scope pair rules as PostgreSQL.
func (s Scope) Validate() error {
	switch s.Type {
	case ScopeGlobal, ScopeEnvironment, ScopeProject, ScopeResourceGroup, ScopeSwitchGroup, ScopeSpecificResource:
	default:
		return fmt.Errorf("unsupported scope type %q", s.Type)
	}
	if s.Type == ScopeGlobal {
		if s.ID != "" {
			return errors.New("global scope ID must be empty")
		}
		return nil
	}
	if strings.TrimSpace(s.ID) == "" {
		return errors.New("non-global scope ID is required")
	}
	return nil
}

// Covers reports whether a binding scope authorizes the target scope.
// V1 deliberately supports only GLOBAL or exact matching; hierarchy expansion
// requires explicit resource metadata and is deferred.
func (s Scope) Covers(target Scope) bool {
	if s.Validate() != nil || target.Validate() != nil {
		return false
	}
	return s.Type == ScopeGlobal || (s.Type == target.Type && s.ID == target.ID)
}

// Binding grants one role and its resolved permissions within a scope.
type Binding struct {
	Role        Role
	Scope       Scope
	Permissions []Permission
}

// Validate rejects malformed or duplicate permission entries.
func (b Binding) Validate() error {
	if err := b.Role.Validate(); err != nil {
		return err
	}
	if err := b.Scope.Validate(); err != nil {
		return err
	}
	seen := make(map[Permission]struct{}, len(b.Permissions))
	for _, permission := range b.Permissions {
		if err := permission.Validate(); err != nil {
			return err
		}
		if _, exists := seen[permission]; exists {
			return fmt.Errorf("duplicate permission %q", permission)
		}
		seen[permission] = struct{}{}
	}
	return nil
}

// Principal is the authenticated local user plus database-resolved RBAC data.
type Principal struct {
	UserID         string
	Subject        string
	Username       string
	ServiceActorID string
	Bindings       []Binding
}

// Validate enforces identity and binding invariants.
func (p Principal) Validate() error {
	if strings.TrimSpace(p.UserID) == "" {
		return errors.New("principal user ID is required")
	}
	if strings.TrimSpace(p.Subject) == "" {
		return errors.New("principal subject is required")
	}
	if strings.TrimSpace(p.Username) == "" {
		return errors.New("principal username is required")
	}
	for i, binding := range p.Bindings {
		if err := binding.Validate(); err != nil {
			return fmt.Errorf("binding %d: %w", i, err)
		}
	}
	return nil
}

// Authorize returns the effective role that grants permission for target.
func (p Principal) Authorize(permission Permission, target Scope) (Role, bool) {
	if p.Validate() != nil || permission.Validate() != nil || target.Validate() != nil {
		return "", false
	}
	for _, binding := range p.Bindings {
		if !binding.Scope.Covers(target) {
			continue
		}
		for _, candidate := range binding.Permissions {
			if candidate == permission {
				return binding.Role, true
			}
		}
	}
	return "", false
}

// Roles returns stable, deduplicated role names for introspection responses.
func (p Principal) Roles() []Role {
	seen := make(map[Role]struct{})
	for _, binding := range p.Bindings {
		seen[binding.Role] = struct{}{}
	}
	roles := make([]Role, 0, len(seen))
	for role := range seen {
		roles = append(roles, role)
	}
	sort.Slice(roles, func(i, j int) bool { return roles[i] < roles[j] })
	return roles
}

// PrincipalRepository resolves local authorization from a verified JWT subject.
type PrincipalRepository interface {
	ResolveBySubject(context.Context, string) (Principal, error)
}
