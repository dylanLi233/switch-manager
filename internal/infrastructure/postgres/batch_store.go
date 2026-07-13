package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/dylanLi233/switch-manager/internal/domain/task"
	"github.com/dylanLi233/switch-manager/internal/operationsvc"
	"github.com/jackc/pgx/v5"
)

const batchColumns = `
	id::text, parent_task_id::text, operation, continue_on_failure,
	total_count, success_count, failed_count, cancelled_count, status,
	created_by::text, created_at, updated_at`

type BatchStore struct{ store *Store }

func NewBatchStore(store *Store) (*BatchStore, error) {
	if store == nil || store.pool == nil {
		return nil, errors.New("PostgreSQL store is required")
	}
	return &BatchStore{store: store}, nil
}

func (s *BatchStore) CreateBatch(ctx context.Context, commit operationsvc.BatchCommit) (task.BatchSnapshot, error) {
	if ctx == nil {
		return task.BatchSnapshot{}, errors.New("context is required")
	}
	if err := commit.Parent.Validate(); err != nil {
		return task.BatchSnapshot{}, apperror.Wrap(apperror.CodeValidationError, "", err)
	}
	if err := commit.Batch.Validate(); err != nil {
		return task.BatchSnapshot{}, apperror.Wrap(apperror.CodeValidationError, "", err)
	}
	if commit.Parent.Type != task.TypeBatchParent || commit.Parent.ID != commit.Batch.ParentTaskID || commit.Batch.ID != commit.Parent.TargetID {
		return task.BatchSnapshot{}, apperror.New(apperror.CodeValidationError, "batch parent is inconsistent")
	}
	if len(commit.Children) != commit.Batch.TotalCount || len(commit.Items) != len(commit.Children) || len(commit.Audits) != len(commit.Children) {
		return task.BatchSnapshot{}, apperror.New(apperror.CodeValidationError, "batch child collections are inconsistent")
	}
	var created task.BatchSnapshot
	err := s.store.WithinTx(ctx, func(repositories *Repositories) error {
		parent, err := repositories.Tasks.Create(ctx, commit.Parent)
		if err != nil {
			return err
		}
		commit.ParentAudit.TaskID = parent.ID
		if _, err := repositories.Audits.Create(ctx, commit.ParentAudit); err != nil {
			return apperror.Wrap(apperror.CodeAuditUnavailable, "", err)
		}
		if _, err := repositories.Tasks.q.Exec(ctx, `
			INSERT INTO batch_tasks (
				id, parent_task_id, operation, continue_on_failure, total_count,
				success_count, failed_count, cancelled_count, status,
				created_by, created_at, updated_at
			) VALUES ($1::uuid,$2::uuid,$3,$4,$5,$6,$7,$8,$9,$10::uuid,$11,$12)`,
			commit.Batch.ID, commit.Batch.ParentTaskID, string(commit.Batch.Operation),
			commit.Batch.ContinueOnFailure, commit.Batch.TotalCount,
			commit.Batch.SuccessCount, commit.Batch.FailedCount, commit.Batch.CancelledCount,
			string(commit.Batch.Status), commit.Batch.CreatedBy, commit.Batch.CreatedAt, commit.Batch.UpdatedAt,
		); err != nil {
			return mapDatabaseError(err, "", "create batch")
		}
		for index := range commit.Children {
			child := commit.Children[index]
			item := commit.Items[index]
			record := commit.Audits[index]
			if err := item.Validate(); err != nil {
				return apperror.Wrap(apperror.CodeValidationError, "", err)
			}
			if child.Type != task.TypeBatchChild || child.ParentTaskID != commit.Batch.ID || item.ChildTaskID != child.ID || item.DeviceID != child.TargetID || item.SequenceNo != index+1 {
				return apperror.New(apperror.CodeValidationError, "batch child is inconsistent")
			}
			createdChild, err := repositories.Tasks.Create(ctx, child)
			if err != nil {
				return err
			}
			record.TaskID = createdChild.ID
			if _, err := repositories.Audits.Create(ctx, record); err != nil {
				return apperror.Wrap(apperror.CodeAuditUnavailable, "", err)
			}
			if _, err := repositories.Tasks.q.Exec(ctx, `
				INSERT INTO batch_task_items(batch_task_id,device_id,child_task_id,sequence_no)
				VALUES ($1::uuid,$2::uuid,$3::uuid,$4)`, item.BatchTaskID, item.DeviceID, item.ChildTaskID, item.SequenceNo); err != nil {
				return mapDatabaseError(err, "", "create batch item")
			}
		}
		created, err = loadBatchSnapshot(ctx, repositories.Tasks.q, commit.Batch.ID)
		return err
	})
	if err != nil {
		return task.BatchSnapshot{}, err
	}
	return created, nil
}

func (s *BatchStore) GetBatch(ctx context.Context, id string) (task.BatchSnapshot, error) {
	if ctx == nil {
		return task.BatchSnapshot{}, errors.New("context is required")
	}
	return loadBatchSnapshot(ctx, s.store.pool, strings.TrimSpace(id))
}

func (s *BatchStore) ListOpenBatchIDs(ctx context.Context, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	rows, err := s.store.pool.Query(ctx, `
		SELECT id::text FROM batch_tasks
		WHERE status NOT IN ('SUCCESS','PARTIAL_SUCCESS','FAILED','CANCELLED','INTERRUPTED')
		ORDER BY created_at,id LIMIT $1`, limit)
	if err != nil {
		return nil, mapDatabaseError(err, "", "list open batches")
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, mapDatabaseError(err, "", "scan open batch")
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, mapDatabaseError(err, "", "iterate open batches")
	}
	return ids, nil
}

func (s *BatchStore) RefreshBatch(ctx context.Context, id string, at time.Time) (task.BatchSnapshot, error) {
	if ctx == nil {
		return task.BatchSnapshot{}, errors.New("context is required")
	}
	if strings.TrimSpace(id) == "" || at.IsZero() {
		return task.BatchSnapshot{}, apperror.New(apperror.CodeValidationError, "batch ID and refresh time are required")
	}
	var refreshed task.BatchSnapshot
	err := s.store.WithinTx(ctx, func(repositories *Repositories) error {
		var lockedID string
		if err := repositories.Tasks.q.QueryRow(ctx, `SELECT id::text FROM batch_tasks WHERE id=$1::uuid FOR UPDATE`, id).Scan(&lockedID); err != nil {
			return mapDatabaseError(err, apperror.CodeResourceNotFound, "lock batch")
		}
		current, err := loadBatchSnapshot(ctx, repositories.Tasks.q, id)
		if err != nil {
			return err
		}
		if !current.Batch.ContinueOnFailure && hasFailedChild(current.Items) {
			if _, err := repositories.Tasks.q.Exec(ctx, `
				UPDATE tasks AS t SET
					status='CANCELLED', cancel_requested_at=$2, finished_at=$2, version=t.version+1
				FROM batch_task_items AS i
				WHERE i.batch_task_id=$1::uuid AND i.child_task_id=t.id
				  AND t.status IN ('PENDING','QUEUED')`, id, at); err != nil {
				return mapDatabaseError(err, "", "cancel unstarted batch children")
			}
			current, err = loadBatchSnapshot(ctx, repositories.Tasks.q, id)
			if err != nil {
				return err
			}
		}
		statuses := make([]task.Status, len(current.Items))
		successCount, failedCount, cancelledCount := 0, 0, 0
		for index, item := range current.Items {
			statuses[index] = item.Child.Status
			switch item.Child.Status {
			case task.StatusSuccess:
				successCount++
			case task.StatusFailed, task.StatusPartialSuccess, task.StatusInterrupted:
				failedCount++
			case task.StatusCancelled:
				cancelledCount++
			}
		}
		aggregate, err := task.AggregateStatus(statuses)
		if err != nil {
			return apperror.Wrap(apperror.CodeInternalError, "", err)
		}
		if _, err := repositories.Tasks.q.Exec(ctx, `
			UPDATE batch_tasks SET success_count=$2,failed_count=$3,cancelled_count=$4,status=$5,updated_at=$6
			WHERE id=$1::uuid`, id, successCount, failedCount, cancelledCount, string(aggregate), at); err != nil {
			return mapDatabaseError(err, "", "update batch aggregate")
		}
		summary, _ := json.Marshal(map[string]any{"batch_id": id, "total_count": len(current.Items), "success_count": successCount, "failed_count": failedCount, "cancelled_count": cancelledCount, "status": aggregate})
		parentStatus := task.StatusRunning
		finishedAt := any(nil)
		errorCode := ""
		if aggregate.Terminal() {
			parentStatus = aggregate
			finishedAt = at
			switch aggregate {
			case task.StatusFailed:
				errorCode = task.BatchErrorFailed
			case task.StatusPartialSuccess:
				errorCode = task.BatchErrorPartial
			}
		}
		if _, err := repositories.Tasks.q.Exec(ctx, `
			UPDATE tasks SET status=$2,result=$3::jsonb,error_code=NULLIF($4,''),finished_at=$5,version=version+1
			WHERE id=$1::uuid AND task_type='BATCH_PARENT' AND status NOT IN ('SUCCESS','PARTIAL_SUCCESS','FAILED','CANCELLED','INTERRUPTED')`,
			current.Parent.ID, string(parentStatus), summary, errorCode, finishedAt); err != nil {
			return mapDatabaseError(err, "", "update batch parent")
		}
		if aggregate.Terminal() {
			if _, err := repositories.Audits.Complete(ctx, current.Parent.ID, string(aggregate), errorCode, summary, at); err != nil {
				return apperror.Wrap(apperror.CodeAuditUnavailable, "", err)
			}
		}
		refreshed, err = loadBatchSnapshot(ctx, repositories.Tasks.q, id)
		return err
	})
	if err != nil {
		return task.BatchSnapshot{}, err
	}
	return refreshed, nil
}

func hasFailedChild(items []task.BatchItemState) bool {
	for _, item := range items {
		switch item.Child.Status {
		case task.StatusFailed, task.StatusPartialSuccess, task.StatusInterrupted:
			return true
		}
	}
	return false
}

func loadBatchSnapshot(ctx context.Context, q DBTX, id string) (task.BatchSnapshot, error) {
	var snapshot task.BatchSnapshot
	var operationName, status string
	row := q.QueryRow(ctx, `SELECT `+batchColumns+` FROM batch_tasks WHERE id=$1::uuid`, id)
	if err := row.Scan(&snapshot.Batch.ID, &snapshot.Batch.ParentTaskID, &operationName,
		&snapshot.Batch.ContinueOnFailure, &snapshot.Batch.TotalCount,
		&snapshot.Batch.SuccessCount, &snapshot.Batch.FailedCount, &snapshot.Batch.CancelledCount,
		&status, &snapshot.Batch.CreatedBy, &snapshot.Batch.CreatedAt, &snapshot.Batch.UpdatedAt); err != nil {
		return task.BatchSnapshot{}, mapDatabaseError(err, apperror.CodeResourceNotFound, "get batch")
	}
	snapshot.Batch.Operation = operationName
	snapshot.Batch.Status = task.Status(status)
	parent, err := scanTask(q.QueryRow(ctx, `SELECT `+taskColumns+` FROM tasks WHERE id=$1::uuid`, snapshot.Batch.ParentTaskID))
	if err != nil {
		return task.BatchSnapshot{}, mapDatabaseError(err, apperror.CodeTaskNotFound, "get batch parent")
	}
	snapshot.Parent = parent
	rows, err := q.Query(ctx, `
		SELECT i.batch_task_id::text,i.device_id::text,i.child_task_id::text,i.sequence_no,`+taskColumns+`
		FROM batch_task_items AS i JOIN tasks AS t ON t.id=i.child_task_id
		WHERE i.batch_task_id=$1::uuid ORDER BY i.sequence_no`, id)
	if err != nil {
		return task.BatchSnapshot{}, mapDatabaseError(err, "", "list batch items")
	}
	defer rows.Close()
	items := make([]task.BatchItemState, 0, snapshot.Batch.TotalCount)
	for rows.Next() {
		var item task.BatchItem
		var child task.Persisted
		var taskType, childStatus, executionMode string
		var payload, resultPayload []byte
		var startedAt, childFinishedAt, cancelRequestedAt sql.NullTime
		if err := rows.Scan(&item.BatchTaskID, &item.DeviceID, &item.ChildTaskID, &item.SequenceNo,
			&child.ID, &child.ParentTaskID, &taskType, &child.Operation, &child.TargetType,
			&child.TargetID, &childStatus, &executionMode, &child.DryRun, &child.SaveConfig,
			&child.IdempotencyKey, &payload, &resultPayload, &child.ErrorCode, &child.CreatedBy,
			&child.RetryOf, &child.PluginName, &child.PluginVersion, &child.CreatedAt,
			&startedAt, &childFinishedAt, &cancelRequestedAt, &child.Version); err != nil {
			return task.BatchSnapshot{}, mapDatabaseError(err, "", "scan batch item")
		}
		child.Type = task.Type(taskType)
		child.Status = task.Status(childStatus)
		child.ExecutionMode = executionMode
		child.Payload = append(json.RawMessage(nil), payload...)
		child.Result = append(json.RawMessage(nil), resultPayload...)
		child.StartedAt = timePointer(startedAt)
		child.FinishedAt = timePointer(childFinishedAt)
		child.CancelRequestedAt = timePointer(cancelRequestedAt)
		items = append(items, task.BatchItemState{Item: item, Child: child})
	}
	if err := rows.Err(); err != nil {
		return task.BatchSnapshot{}, mapDatabaseError(err, "", "iterate batch items")
	}
	snapshot.Items = items
	if err := snapshot.Validate(); err != nil {
		return task.BatchSnapshot{}, fmt.Errorf("invalid batch snapshot: %w", err)
	}
	return snapshot, nil
}

var _ operationsvc.BatchPersistence = (*BatchStore)(nil)
var _ = pgx.ErrNoRows
