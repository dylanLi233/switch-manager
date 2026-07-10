package postgres

import (
	"context"
	"errors"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func mapDatabaseError(err error, notFound apperror.Code, operation string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		if notFound == "" {
			notFound = apperror.CodeResourceNotFound
		}
		return apperror.Wrap(notFound, "", err)
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			if pgErr.ConstraintName == "tasks_actor_idempotency_uq" {
				return apperror.Wrap(apperror.CodeIdempotencyConflict, "", err)
			}
			return apperror.Wrap(apperror.CodeStateConflict, "", err)
		case "23503":
			return apperror.Wrap(apperror.CodeStateConflict, "", err)
		case "23514", "22P02", "22023":
			return apperror.Wrap(apperror.CodeValidationError, "", err)
		case "40001", "40P01", "08000", "08003", "08006", "08001", "08004":
			return apperror.Wrap(apperror.CodeDatabaseUnavailable, "", err)
		}
	}
	_ = operation // reserved for trusted structured logging at the application edge.
	return apperror.Wrap(apperror.CodeDatabaseUnavailable, "", err)
}
