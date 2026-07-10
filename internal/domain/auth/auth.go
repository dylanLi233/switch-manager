// Package auth defines authenticated actors and platform roles.
package auth

import (
	"errors"
	"fmt"
	"strings"
)

// Role identifies a built-in V1 platform role.
type Role string

const (
	// RoleViewer can inspect resources and run approved read-only operations.
	RoleViewer Role = "VIEWER"
	// RoleAdmin can manage resources and run configuration operations.
	RoleAdmin Role = "ADMIN"
	// RoleAuditor can inspect audit records but cannot operate devices.
	RoleAuditor Role = "AUDITOR"
)

// Validate reports whether the role is one of the supported V1 roles.
func (r Role) Validate() error {
	switch r {
	case RoleViewer, RoleAdmin, RoleAuditor:
		return nil
	default:
		return fmt.Errorf("unsupported role %q", r)
	}
}

// Actor is the authenticated identity responsible for an operation.
type Actor struct {
	UserID         string
	Username       string
	Role           Role
	ServiceActorID string
	SourceIP       string
}

// Validate enforces the minimum trusted identity fields required by the domain.
func (a Actor) Validate() error {
	if strings.TrimSpace(a.UserID) == "" {
		return errors.New("actor user ID is required")
	}
	if strings.TrimSpace(a.Username) == "" {
		return errors.New("actor username is required")
	}
	if err := a.Role.Validate(); err != nil {
		return fmt.Errorf("validate actor role: %w", err)
	}
	return nil
}
