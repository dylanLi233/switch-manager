package postgres

import (
	"context"
	"errors"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/dylanLi233/switch-manager/internal/domain/audit"
	"github.com/dylanLi233/switch-manager/internal/domain/task"
)

// OperationSubmission atomically creates an operation task and its mandatory audit record.
type OperationSubmission struct{ store *Store }

func NewOperationSubmission(store *Store) (*OperationSubmission, error) {
	if store == nil || store.pool == nil {
		return nil, errors.New("PostgreSQL store is required")
	}
	return &OperationSubmission{store: store}, nil
}

func (s *OperationSubmission) CreateTaskAndAudit(ctx context.Context, value task.Persisted, record audit.Record) (task.Persisted, error) {
	if ctx == nil {
		return task.Persisted{}, errors.New("context is required")
	}
	var created task.Persisted
	err := s.store.WithinTx(ctx, func(repositories *Repositories) error {
		result, err := repositories.Tasks.Create(ctx, value)
		if err != nil {
			return err
		}
		record.TaskID = result.ID
		if _, err := repositories.Audits.Create(ctx, record); err != nil {
			return apperror.Wrap(apperror.CodeAuditUnavailable, "", err)
		}
		created = result
		return nil
	})
	if err != nil {
		return task.Persisted{}, err
	}
	return created, nil
}
