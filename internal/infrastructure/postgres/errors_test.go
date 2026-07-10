package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestMapDatabaseError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		code apperror.Code
	}{
		{"not found", pgx.ErrNoRows, apperror.CodeDeviceNotFound},
		{"unique", &pgconn.PgError{Code: "23505", ConstraintName: "switches_host_port_active_uq"}, apperror.CodeStateConflict},
		{"idempotency", &pgconn.PgError{Code: "23505", ConstraintName: "tasks_actor_idempotency_uq"}, apperror.CodeIdempotencyConflict},
		{"check", &pgconn.PgError{Code: "23514"}, apperror.CodeValidationError},
		{"deadlock", &pgconn.PgError{Code: "40P01"}, apperror.CodeDatabaseUnavailable},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			notFound := apperror.CodeDeviceNotFound
			got := mapDatabaseError(tc.err, notFound, tc.name)
			if !apperror.IsCode(got, tc.code) {
				t.Fatalf("code mismatch: %v", got)
			}
		})
	}
}

func TestMapDatabaseErrorPreservesContextCancellation(t *testing.T) {
	t.Parallel()
	if got := mapDatabaseError(context.Canceled, "", "query"); !errors.Is(got, context.Canceled) {
		t.Fatalf("got %v", got)
	}
}
