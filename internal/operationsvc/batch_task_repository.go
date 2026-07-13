package operationsvc

import (
	"context"
	"errors"
	"time"

	"github.com/dylanLi233/switch-manager/internal/domain/task"
)

// BatchAwareTaskRepository forwards all task operations and reconciles a batch
// whenever a child reaches a terminal state, is cancelled, or recovery
// interrupts running children.
type BatchAwareTaskRepository struct {
	task.Repository
	batches BatchPersistence
	now     func() time.Time
}

func NewBatchAwareTaskRepository(base task.Repository, batches BatchPersistence) (*BatchAwareTaskRepository, error) {
	if base == nil {
		return nil, errors.New("task repository is required")
	}
	if batches == nil {
		return nil, errors.New("batch persistence is required")
	}
	return &BatchAwareTaskRepository{Repository: base, batches: batches, now: time.Now}, nil
}

func (r *BatchAwareTaskRepository) Save(ctx context.Context, value task.Persisted, expectedVersion int64) (task.Persisted, error) {
	saved, err := r.Repository.Save(ctx, value, expectedVersion)
	if err != nil {
		return task.Persisted{}, err
	}
	if saved.Type == task.TypeBatchChild && saved.Status.Terminal() {
		if _, err := r.batches.RefreshBatch(ctx, saved.ParentTaskID, r.now().UTC()); err != nil {
			return saved, err
		}
	}
	return saved, nil
}

func (r *BatchAwareTaskRepository) RequestCancel(ctx context.Context, id string, at time.Time) (task.Persisted, error) {
	value, err := r.Repository.RequestCancel(ctx, id, at)
	if err != nil {
		return task.Persisted{}, err
	}
	if value.Type == task.TypeBatchChild && value.Status.Terminal() {
		if _, err := r.batches.RefreshBatch(ctx, value.ParentTaskID, r.now().UTC()); err != nil {
			return value, err
		}
	}
	return value, nil
}

func (r *BatchAwareTaskRepository) InterruptRunning(ctx context.Context, at time.Time) (int64, error) {
	count, err := r.Repository.InterruptRunning(ctx, at)
	if err != nil {
		return 0, err
	}
	ids, err := r.batches.ListOpenBatchIDs(ctx, 1000)
	if err != nil {
		return count, err
	}
	for _, id := range ids {
		if _, err := r.batches.RefreshBatch(ctx, id, r.now().UTC()); err != nil {
			return count, err
		}
	}
	return count, nil
}

var _ task.Repository = (*BatchAwareTaskRepository)(nil)
