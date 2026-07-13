package postgres

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/dylanLi233/switch-manager/internal/apperror"
)

// SyncDirectTerminalAudits completes child audits for lifecycle transitions that
// bypass the operation executor, specifically queued cancellation and recovery
// interruption. Executor-completed audits already have finished_at and are not
// modified.
func (s *BatchStore) SyncDirectTerminalAudits(ctx context.Context, batchID string, at time.Time) error {
	if ctx == nil {
		return errors.New("context is required")
	}
	if s == nil || s.store == nil || s.store.pool == nil {
		return errors.New("batch store is not initialized")
	}
	if strings.TrimSpace(batchID) == "" || at.IsZero() {
		return apperror.New(apperror.CodeValidationError, "batch ID and audit time are required")
	}
	_, err := s.store.pool.Exec(ctx, `
		UPDATE audit_logs AS a SET
			status=t.status,
			error_code=t.error_code,
			result_summary_redacted=jsonb_build_object(
				'status', t.status,
				'error_code', COALESCE(t.error_code,''),
				'direct_lifecycle_transition', true
			),
			finished_at=COALESCE(t.finished_at,$2)
		FROM tasks AS t
		JOIN batch_task_items AS i ON i.child_task_id=t.id
		WHERE i.batch_task_id=$1::uuid
		  AND a.task_id=t.id
		  AND t.status IN ('CANCELLED','INTERRUPTED')
		  AND a.finished_at IS NULL`, batchID, at)
	if err != nil {
		return mapDatabaseError(err, "", "synchronize batch child audits")
	}
	return nil
}
