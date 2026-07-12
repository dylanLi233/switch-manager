package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/dylanLi233/switch-manager/internal/domain/operation"
	"github.com/dylanLi233/switch-manager/internal/domain/task"
)

type memoryRepository struct {
	mu     sync.Mutex
	values map[string]task.Persisted
}

func newMemoryRepository(values ...task.Persisted) *memoryRepository {
	repository := &memoryRepository{values: make(map[string]task.Persisted)}
	for _, value := range values {
		repository.values[value.ID] = cloneTask(value)
	}
	return repository
}

func cloneTask(value task.Persisted) task.Persisted {
	value.Payload = append(json.RawMessage(nil), value.Payload...)
	value.Result = append(json.RawMessage(nil), value.Result...)
	return value
}

func (r *memoryRepository) Create(_ context.Context, value task.Persisted) (task.Persisted, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.values[value.ID]; exists {
		return task.Persisted{}, apperror.New(apperror.CodeStateConflict, "task already exists")
	}
	r.values[value.ID] = cloneTask(value)
	return cloneTask(value), nil
}

func (r *memoryRepository) Get(_ context.Context, id string) (task.Persisted, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	value, exists := r.values[id]
	if !exists {
		return task.Persisted{}, apperror.New(apperror.CodeTaskNotFound, "")
	}
	return cloneTask(value), nil
}

func (r *memoryRepository) FindByIdempotency(context.Context, string, string) (task.Persisted, error) {
	return task.Persisted{}, apperror.New(apperror.CodeTaskNotFound, "")
}

func (r *memoryRepository) Save(_ context.Context, value task.Persisted, expectedVersion int64) (task.Persisted, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, exists := r.values[value.ID]
	if !exists {
		return task.Persisted{}, apperror.New(apperror.CodeTaskNotFound, "")
	}
	if current.Version != expectedVersion {
		return task.Persisted{}, apperror.New(apperror.CodeStateConflict, "")
	}
	r.values[value.ID] = cloneTask(value)
	return cloneTask(value), nil
}

func (r *memoryRepository) ListRecoverable(_ context.Context, limit int) ([]task.Persisted, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]task.Persisted, 0)
	for _, value := range r.values {
		if value.Status == task.StatusPending || value.Status == task.StatusQueued {
			result = append(result, cloneTask(value))
			if len(result) == limit {
				break
			}
		}
	}
	return result, nil
}

func (r *memoryRepository) QueuePending(_ context.Context, id string) (task.Persisted, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	value, exists := r.values[id]
	if !exists {
		return task.Persisted{}, apperror.New(apperror.CodeTaskNotFound, "")
	}
	if value.Status == task.StatusQueued {
		return cloneTask(value), nil
	}
	if value.Status != task.StatusPending {
		return task.Persisted{}, apperror.New(apperror.CodeStateConflict, "")
	}
	value.Status = task.StatusQueued
	value.Version++
	r.values[id] = value
	return cloneTask(value), nil
}

func (r *memoryRepository) RequestCancel(_ context.Context, id string, at time.Time) (task.Persisted, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	value, exists := r.values[id]
	if !exists {
		return task.Persisted{}, apperror.New(apperror.CodeTaskNotFound, "")
	}
	if value.Status == task.StatusCancelled || (value.Status == task.StatusRunning && value.CancelRequestedAt != nil) {
		return cloneTask(value), nil
	}
	switch value.Status {
	case task.StatusPending, task.StatusQueued:
		value.Status = task.StatusCancelled
		value.FinishedAt = &at
		value.CancelRequestedAt = &at
		value.Version++
	case task.StatusRunning:
		value.CancelRequestedAt = &at
		value.Version++
	default:
		return task.Persisted{}, apperror.New(apperror.CodeTaskNotCancellable, "")
	}
	r.values[id] = value
	return cloneTask(value), nil
}

func (r *memoryRepository) ClaimNextQueued(_ context.Context, at time.Time) (task.Persisted, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, value := range r.values {
		if value.Status == task.StatusQueued {
			value.Status = task.StatusRunning
			value.StartedAt = &at
			value.Version++
			r.values[id] = value
			return cloneTask(value), nil
		}
	}
	return task.Persisted{}, task.ErrNoQueuedTask
}

func (r *memoryRepository) InterruptRunning(_ context.Context, at time.Time) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var count int64
	for id, value := range r.values {
		if value.Status == task.StatusRunning {
			value.Status = task.StatusInterrupted
			value.FinishedAt = &at
			value.Version++
			r.values[id] = value
			count++
		}
	}
	return count, nil
}

func schedulerTask(id string, status task.Status) task.Persisted {
	created := time.Now().Add(-time.Minute).UTC()
	value := task.Persisted{Task: task.Task{
		ID: id, Type: task.TypeOperation, Operation: operation.Name("diagnostic.echo"),
		TargetType: "DEVICE", TargetID: "device-1", Status: status,
		ExecutionMode: operation.ExecutionModeAsync, Payload: json.RawMessage(`{}`),
		CreatedBy: "user-1", CreatedAt: created, Version: 1,
	}}
	if status == task.StatusRunning {
		started := created.Add(time.Second)
		value.StartedAt = &started
	}
	if status.Terminal() {
		started := created.Add(time.Second)
		finished := started.Add(time.Second)
		value.StartedAt, value.FinishedAt = &started, &finished
		if status == task.StatusFailed || status == task.StatusPartialSuccess {
			value.ErrorCode = "TEST_ERROR"
		}
	}
	return value
}

func runScheduler(t *testing.T, scheduler *Scheduler) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- scheduler.Run(ctx) }()
	return cancel, done
}

func waitTaskStatus(t *testing.T, repository *memoryRepository, id string, want task.Status) task.Persisted {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		value, _ := repository.Get(context.Background(), id)
		if value.Status == want {
			return value
		}
		if time.Now().After(deadline) {
			t.Fatalf("task %s status = %s, want %s", id, value.Status, want)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestRecoverQueuesPendingAndInterruptsRunning(t *testing.T) {
	repository := newMemoryRepository(
		schedulerTask("pending", task.StatusPending),
		schedulerTask("running", task.StatusRunning),
	)
	handler := HandlerFunc(func(context.Context, task.Persisted) (ExecutionResult, error) {
		return ExecutionResult{Status: task.StatusSuccess, Result: json.RawMessage(`{"ok":true}`)}, nil
	})
	scheduler, err := New(repository, handler, Config{Workers: 1, PollInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	cancel, done := runScheduler(t, scheduler)
	waitTaskStatus(t, repository, "pending", task.StatusSuccess)
	waitTaskStatus(t, repository, "running", task.StatusInterrupted)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestCancelQueuedTaskDoesNotExecute(t *testing.T) {
	repository := newMemoryRepository(schedulerTask("queued", task.StatusQueued))
	scheduler, _ := New(repository, HandlerFunc(func(context.Context, task.Persisted) (ExecutionResult, error) {
		t.Fatal("cancelled queued task must not execute")
		return ExecutionResult{}, nil
	}), Config{})
	value, err := scheduler.Cancel(context.Background(), "queued")
	if err != nil {
		t.Fatal(err)
	}
	if value.Status != task.StatusCancelled || value.FinishedAt == nil {
		t.Fatalf("cancelled task = %+v", value)
	}
}

func TestCancelRunningTaskCancelsHandler(t *testing.T) {
	repository := newMemoryRepository(schedulerTask("pending", task.StatusPending))
	started := make(chan struct{})
	handler := HandlerFunc(func(ctx context.Context, _ task.Persisted) (ExecutionResult, error) {
		close(started)
		<-ctx.Done()
		return ExecutionResult{}, ctx.Err()
	})
	scheduler, _ := New(repository, handler, Config{Workers: 1, PollInterval: time.Millisecond})
	cancelRun, done := runScheduler(t, scheduler)
	<-started
	if _, err := scheduler.Cancel(context.Background(), "pending"); err != nil {
		t.Fatal(err)
	}
	value := waitTaskStatus(t, repository, "pending", task.StatusCancelled)
	if value.CancelRequestedAt == nil {
		t.Fatal("expected cancellation request timestamp")
	}
	cancelRun()
	<-done
}

func TestRetryCreatesNewQueuedTask(t *testing.T) {
	repository := newMemoryRepository(schedulerTask("source", task.StatusFailed))
	scheduler, _ := New(repository, HandlerFunc(func(context.Context, task.Persisted) (ExecutionResult, error) {
		return ExecutionResult{Status: task.StatusSuccess}, nil
	}), Config{})
	value, err := scheduler.Retry(context.Background(), RetryRequest{
		SourceTaskID: "source", NewTaskID: "retry", CreatedBy: "user-2", CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if value.ID != "retry" || value.RetryOf != "source" || value.Status != task.StatusQueued {
		t.Fatalf("retry task = %+v", value)
	}
	if len(value.Result) != 0 || value.ErrorCode != "" || value.IdempotencyKey != "" || value.PluginName != "" {
		t.Fatalf("retry retained execution state: %+v", value)
	}
}

func TestShutdownMarksRunningTaskInterrupted(t *testing.T) {
	repository := newMemoryRepository(schedulerTask("pending", task.StatusPending))
	started := make(chan struct{})
	handler := HandlerFunc(func(ctx context.Context, _ task.Persisted) (ExecutionResult, error) {
		close(started)
		<-ctx.Done()
		return ExecutionResult{}, ctx.Err()
	})
	scheduler, _ := New(repository, handler, Config{Workers: 1, PollInterval: time.Millisecond})
	cancel, done := runScheduler(t, scheduler)
	<-started
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	waitTaskStatus(t, repository, "pending", task.StatusInterrupted)
}

func TestHandlerPanicFailsTaskAndWorkerContinues(t *testing.T) {
	repository := newMemoryRepository(
		schedulerTask("first", task.StatusPending),
		schedulerTask("second", task.StatusPending),
	)
	var mu sync.Mutex
	calls := 0
	handler := HandlerFunc(func(context.Context, task.Persisted) (ExecutionResult, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls == 1 {
			panic("boom")
		}
		return ExecutionResult{Status: task.StatusSuccess}, nil
	})
	scheduler, _ := New(repository, handler, Config{Workers: 1, PollInterval: time.Millisecond})
	cancel, done := runScheduler(t, scheduler)
	deadline := time.Now().Add(2 * time.Second)
	for {
		first, _ := repository.Get(context.Background(), "first")
		second, _ := repository.Get(context.Background(), "second")
		if first.Status.Terminal() && second.Status.Terminal() {
			if (first.Status == task.StatusFailed && second.Status == task.StatusSuccess) ||
				(second.Status == task.StatusFailed && first.Status == task.StatusSuccess) {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("first=%s second=%s", first.Status, second.Status)
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
}

func TestHandlerApplicationErrorPersistsOnlyCode(t *testing.T) {
	repository := newMemoryRepository(schedulerTask("pending", task.StatusPending))
	handler := HandlerFunc(func(context.Context, task.Persisted) (ExecutionResult, error) {
		return ExecutionResult{}, apperror.Wrap(apperror.CodeCommandTimeout, "secret output", errors.New("password=secret"))
	})
	scheduler, _ := New(repository, handler, Config{Workers: 1, PollInterval: time.Millisecond})
	cancel, done := runScheduler(t, scheduler)
	value := waitTaskStatus(t, repository, "pending", task.StatusFailed)
	if value.ErrorCode != string(apperror.CodeCommandTimeout) {
		t.Fatalf("error code = %q", value.ErrorCode)
	}
	if len(value.Result) != 0 {
		t.Fatalf("unexpected result = %s", value.Result)
	}
	cancel()
	<-done
}

func TestNonRetryableTaskRejected(t *testing.T) {
	repository := newMemoryRepository(schedulerTask("success", task.StatusSuccess))
	scheduler, _ := New(repository, HandlerFunc(func(context.Context, task.Persisted) (ExecutionResult, error) {
		return ExecutionResult{}, nil
	}), Config{})
	_, err := scheduler.Retry(context.Background(), RetryRequest{
		SourceTaskID: "success", NewTaskID: "retry", CreatedBy: "user-1",
	})
	if !apperror.IsCode(err, apperror.CodeTaskNotCancellable) {
		t.Fatalf("Retry() error = %v", err)
	}
}

func TestRunTwiceRejected(t *testing.T) {
	repository := newMemoryRepository()
	scheduler, _ := New(repository, HandlerFunc(func(context.Context, task.Persisted) (ExecutionResult, error) {
		return ExecutionResult{}, nil
	}), Config{PollInterval: time.Second})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- scheduler.Run(ctx) }()
	deadline := time.Now().Add(time.Second)
	for !scheduler.started.Load() {
		if time.Now().After(deadline) {
			t.Fatal("scheduler did not start")
		}
	}
	if err := scheduler.Run(context.Background()); err == nil {
		t.Fatal("expected duplicate Run rejection")
	}
	cancel()
	<-done
}
