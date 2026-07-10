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
	"github.com/dylanLi233/switch-manager/internal/domain/operation"
	"github.com/dylanLi233/switch-manager/internal/domain/task"
	"github.com/jackc/pgx/v5"
)

const taskColumns = `
	id::text, COALESCE(parent_task_id::text,''), task_type, operation,
	target_type, target_id, status, execution_mode, dry_run, save_config,
	COALESCE(idempotency_key,''), payload, result,
	COALESCE(error_code,''), created_by::text, COALESCE(retry_of::text,''),
	COALESCE(plugin_name,''), COALESCE(plugin_version,''), created_at,
	started_at, finished_at, version`

// TaskRepository persists durable tasks and performs optimistic updates.
type TaskRepository struct{ q DBTX }

// Create inserts a task at version 1.
func (r *TaskRepository) Create(ctx context.Context, value task.Persisted) (task.Persisted, error) {
	if len(value.Payload) == 0 {
		value.Payload = json.RawMessage(`{}`)
	}
	if value.Version == 0 {
		value.Version = 1
	}
	if err := value.Validate(); err != nil {
		return task.Persisted{}, apperror.Wrap(apperror.CodeValidationError, "", err)
	}
	row := r.q.QueryRow(ctx, `
		INSERT INTO tasks (
			id, parent_task_id, task_type, operation, target_type, target_id,
			status, execution_mode, dry_run, save_config, idempotency_key,
			payload, result, error_code, created_by,
			retry_of, plugin_name, plugin_version, created_at, started_at,
			finished_at, version
		) VALUES (
			$1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8, $9, $10, $11,
			$12::jsonb, $13::jsonb, $14, $15::uuid, $16::uuid, $17, $18, $19, $20, $21, $22
		) RETURNING `+taskColumns,
		value.ID, nilIfBlank(value.ParentTaskID), string(value.Type), string(value.Operation),
		value.TargetType, value.TargetID, string(value.Status), string(value.ExecutionMode),
		value.DryRun, value.SaveConfig, nilIfBlank(value.IdempotencyKey),
		[]byte(value.Payload), bytesOrNil(value.Result), nilIfBlank(value.ErrorCode),
		value.CreatedBy, nilIfBlank(value.RetryOf), nilIfBlank(value.PluginName),
		nilIfBlank(value.PluginVersion), value.CreatedAt, value.StartedAt,
		value.FinishedAt, value.Version,
	)
	result, err := scanTask(row)
	return result, mapDatabaseError(err, apperror.CodeTaskNotFound, "create task")
}

// Get returns a task by ID.
func (r *TaskRepository) Get(ctx context.Context, id string) (task.Persisted, error) {
	row := r.q.QueryRow(ctx, `SELECT `+taskColumns+` FROM tasks WHERE id=$1::uuid`, id)
	result, err := scanTask(row)
	return result, mapDatabaseError(err, apperror.CodeTaskNotFound, "get task")
}

// FindByIdempotency returns the task bound to an actor and idempotency key.
func (r *TaskRepository) FindByIdempotency(ctx context.Context, createdBy, key string) (task.Persisted, error) {
	if strings.TrimSpace(key) == "" {
		return task.Persisted{}, apperror.Wrap(apperror.CodeValidationError, "", errors.New("idempotency key is required"))
	}
	row := r.q.QueryRow(ctx, `SELECT `+taskColumns+`
		FROM tasks WHERE created_by=$1::uuid AND idempotency_key=$2`, createdBy, key)
	result, err := scanTask(row)
	return result, mapDatabaseError(err, apperror.CodeTaskNotFound, "find task by idempotency")
}

// Save updates a task only when expectedVersion still matches.
func (r *TaskRepository) Save(ctx context.Context, value task.Persisted, expectedVersion int64) (task.Persisted, error) {
	if expectedVersion < 1 || value.Version != expectedVersion+1 {
		return task.Persisted{}, apperror.Wrap(apperror.CodeValidationError, "", fmt.Errorf("task version must advance exactly once"))
	}
	if err := value.Validate(); err != nil {
		return task.Persisted{}, apperror.Wrap(apperror.CodeValidationError, "", err)
	}
	if len(value.Payload) == 0 {
		value.Payload = json.RawMessage(`{}`)
	}
	row := r.q.QueryRow(ctx, `
		UPDATE tasks SET
			parent_task_id=$2::uuid, task_type=$3, operation=$4,
			target_type=$5, target_id=$6, status=$7, execution_mode=$8,
			dry_run=$9, save_config=$10, idempotency_key=$11,
			payload=$12::jsonb, result=$13::jsonb, error_code=$14,
			retry_of=$15::uuid, plugin_name=$16, plugin_version=$17,
			started_at=$18, finished_at=$19, version=$20
		WHERE id=$1::uuid AND version=$21
		RETURNING `+taskColumns,
		value.ID, nilIfBlank(value.ParentTaskID), string(value.Type), string(value.Operation),
		value.TargetType, value.TargetID, string(value.Status), string(value.ExecutionMode),
		value.DryRun, value.SaveConfig, nilIfBlank(value.IdempotencyKey),
		[]byte(value.Payload), bytesOrNil(value.Result), nilIfBlank(value.ErrorCode),
		nilIfBlank(value.RetryOf), nilIfBlank(value.PluginName), nilIfBlank(value.PluginVersion),
		value.StartedAt, value.FinishedAt, value.Version, expectedVersion,
	)
	result, err := scanTask(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return task.Persisted{}, apperror.Wrap(apperror.CodeStateConflict, "", err)
	}
	return result, mapDatabaseError(err, apperror.CodeTaskNotFound, "save task")
}

// ListRecoverable returns tasks that can be queued after process startup.
func (r *TaskRepository) ListRecoverable(ctx context.Context, limit int) ([]task.Persisted, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	rows, err := r.q.Query(ctx, `SELECT `+taskColumns+`
		FROM tasks WHERE status IN ('PENDING','QUEUED')
		ORDER BY created_at, id LIMIT $1`, limit)
	if err != nil {
		return nil, mapDatabaseError(err, "", "list recoverable tasks")
	}
	defer rows.Close()
	result := make([]task.Persisted, 0)
	for rows.Next() {
		value, scanErr := scanTask(rows)
		if scanErr != nil {
			return nil, mapDatabaseError(scanErr, "", "scan recoverable task")
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, mapDatabaseError(err, "", "iterate recoverable tasks")
	}
	return result, nil
}

// ClaimNextQueued atomically transitions the oldest queued task to RUNNING.
func (r *TaskRepository) ClaimNextQueued(ctx context.Context, startedAt time.Time) (task.Persisted, error) {
	if startedAt.IsZero() {
		return task.Persisted{}, apperror.Wrap(apperror.CodeValidationError, "", errors.New("task start time is required"))
	}
	row := r.q.QueryRow(ctx, `
		WITH candidate AS (
			SELECT id FROM tasks
			WHERE status='QUEUED'
			ORDER BY created_at, id
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE tasks AS t
		SET status='RUNNING', started_at=$1, version=t.version+1
		FROM candidate
		WHERE t.id=candidate.id
		RETURNING `+taskColumns, startedAt)
	result, err := scanTask(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return task.Persisted{}, task.ErrNoQueuedTask
	}
	return result, mapDatabaseError(err, "", "claim queued task")
}

// InterruptRunning marks all RUNNING tasks interrupted during process recovery.
func (r *TaskRepository) InterruptRunning(ctx context.Context, finishedAt time.Time) (int64, error) {
	if finishedAt.IsZero() {
		return 0, apperror.Wrap(apperror.CodeValidationError, "", errors.New("interrupt time is required"))
	}
	tag, err := r.q.Exec(ctx, `UPDATE tasks SET
		status='INTERRUPTED', finished_at=$1, version=version+1
		WHERE status='RUNNING' AND started_at <= $1`, finishedAt)
	if err != nil {
		return 0, mapDatabaseError(err, "", "interrupt running tasks")
	}
	return tag.RowsAffected(), nil
}

func scanTask(row rowScanner) (task.Persisted, error) {
	var result task.Persisted
	var taskType, status, executionMode string
	var payload, resultPayload []byte
	var startedAt, finishedAt sql.NullTime
	if err := row.Scan(
		&result.Task.ID, &result.Task.ParentTaskID, &taskType, &result.Task.Operation,
		&result.Task.TargetType, &result.Task.TargetID, &status, &executionMode,
		&result.DryRun, &result.SaveConfig, &result.IdempotencyKey,
		&payload, &resultPayload, &result.Task.ErrorCode, &result.Task.CreatedBy,
		&result.Task.RetryOf, &result.Task.PluginName, &result.Task.PluginVersion,
		&result.Task.CreatedAt, &startedAt, &finishedAt, &result.Task.Version,
	); err != nil {
		return task.Persisted{}, err
	}
	result.Task.Type = task.Type(taskType)
	result.Task.Status = task.Status(status)
	result.Task.ExecutionMode = operation.ExecutionMode(executionMode)
	result.Task.Payload = append(json.RawMessage(nil), payload...)
	result.Task.Result = append(json.RawMessage(nil), resultPayload...)
	result.Task.StartedAt = timePointer(startedAt)
	result.Task.FinishedAt = timePointer(finishedAt)
	if err := result.Validate(); err != nil {
		return task.Persisted{}, fmt.Errorf("invalid task row: %w", err)
	}
	return result, nil
}

var _ task.Repository = (*TaskRepository)(nil)
