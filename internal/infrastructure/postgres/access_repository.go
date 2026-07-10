package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/dylanLi233/switch-manager/internal/domain/auth"
	"github.com/jackc/pgx/v5"
)

// AccessRepository resolves database-backed principals and permission bindings.
type AccessRepository struct{ q DBTX }

// CheckReady verifies that all RBAC tables required by authentication exist.
func (r *AccessRepository) CheckReady(ctx context.Context) error {
	if r == nil || r.q == nil {
		return apperror.Wrap(apperror.CodeDatabaseUnavailable, "", errors.New("access repository is not initialized"))
	}
	var ready bool
	err := r.q.QueryRow(ctx, `
		SELECT
			to_regclass('public.users') IS NOT NULL
			AND to_regclass('public.roles') IS NOT NULL
			AND to_regclass('public.permissions') IS NOT NULL
			AND to_regclass('public.role_permissions') IS NOT NULL
			AND to_regclass('public.user_role_bindings') IS NOT NULL`,
	).Scan(&ready)
	if err != nil {
		return mapDatabaseError(err, "", "check RBAC schema")
	}
	if !ready {
		return apperror.Wrap(apperror.CodeDatabaseUnavailable, "", errors.New("RBAC schema is not ready"))
	}
	return nil
}

// ResolveBySubject loads one active user and all role bindings from PostgreSQL.
func (r *AccessRepository) ResolveBySubject(ctx context.Context, subject string) (auth.Principal, error) {
	if strings.TrimSpace(subject) == "" {
		return auth.Principal{}, apperror.Wrap(apperror.CodeUnauthorized, "", errors.New("JWT subject is required"))
	}
	var principal auth.Principal
	var status string
	err := r.q.QueryRow(ctx, `
		SELECT id::text, external_subject, username, status
		FROM users WHERE external_subject=$1`, subject,
	).Scan(&principal.UserID, &principal.Subject, &principal.Username, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.Principal{}, apperror.Wrap(apperror.CodeForbidden, "", err)
	}
	if err != nil {
		return auth.Principal{}, mapDatabaseError(err, "", "resolve principal user")
	}
	if status != "ACTIVE" {
		return auth.Principal{}, apperror.Wrap(apperror.CodeForbidden, "", errors.New("local user is disabled"))
	}

	rows, err := r.q.Query(ctx, `
		SELECT r.name, b.scope_type, b.scope_id, p.name
		FROM user_role_bindings b
		JOIN roles r ON r.id=b.role_id
		LEFT JOIN role_permissions rp ON rp.role_id=r.id
		LEFT JOIN permissions p ON p.id=rp.permission_id
		WHERE b.user_id=$1::uuid
		ORDER BY r.name, b.scope_type, b.scope_id, p.name`, principal.UserID)
	if err != nil {
		return auth.Principal{}, mapDatabaseError(err, "", "resolve principal bindings")
	}
	defer rows.Close()

	type bindingKey struct {
		role      auth.Role
		scopeType auth.ScopeType
		scopeID   string
	}
	indices := make(map[bindingKey]int)
	for rows.Next() {
		var role string
		var scopeType string
		var scopeID string
		var permission sql.NullString
		if err := rows.Scan(&role, &scopeType, &scopeID, &permission); err != nil {
			return auth.Principal{}, mapDatabaseError(err, "", "scan principal binding")
		}
		key := bindingKey{role: auth.Role(role), scopeType: auth.ScopeType(scopeType), scopeID: scopeID}
		index, exists := indices[key]
		if !exists {
			index = len(principal.Bindings)
			indices[key] = index
			principal.Bindings = append(principal.Bindings, auth.Binding{
				Role: key.role,
				Scope: auth.Scope{Type: key.scopeType, ID: key.scopeID},
			})
		}
		if permission.Valid {
			principal.Bindings[index].Permissions = append(principal.Bindings[index].Permissions, auth.Permission(permission.String))
		}
	}
	if err := rows.Err(); err != nil {
		return auth.Principal{}, mapDatabaseError(err, "", "iterate principal bindings")
	}
	if err := principal.Validate(); err != nil {
		return auth.Principal{}, apperror.Wrap(apperror.CodeInternalError, "", fmt.Errorf("invalid RBAC data: %w", err))
	}
	return principal, nil
}

var _ auth.PrincipalRepository = (*AccessRepository)(nil)
