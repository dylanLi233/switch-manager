package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/dylanLi233/switch-manager/internal/domain/operation"
	"github.com/dylanLi233/switch-manager/internal/domain/task"
	"github.com/dylanLi233/switch-manager/internal/scheduler"
)

const (
	schedulerUserID         = "00000000-0000-0000-0000-000000000301"
	schedulerSuccessTaskID  = "00000000-0000-0000-0000-000000000302"
	schedulerCancelTaskID   = "00000000-0000-0000-0000-000000000303"
	schedulerStaleTaskID    = "00000000-0000-0000-0000-000000000304"
	schedulerRetryTaskID    = "00000000-0000-0000-0000-000000000305"
	schedulerRaceTaskID     = "00000000-0000-0000-0000-000000000306"
	schedulerShutdownTaskID = "00000000-0000-0000-0000-000000000307"
)

func TestTaskSchedulerPostgreSQLIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("TEST_DATABASE_DSN is not set")
	}
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	runMigration(t, root, dsn, "down", "all")
	runMigration(t, root, dsn, "up")

	ctx := context.Background()
	store, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.pool.Exec(ctx, `INSERT INTO users(id, external_subject, username) VALUES ($1::uuid,$2,$3)`, schedulerUserID, "scheduler-user", "scheduler-user"); err != nil {
		t.Fatal(err)
	}

	repository := store.Repositories().Tasks
	createdAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	create := func(id string, status task.Status) task.Persisted {
		value := task.Persisted{Task: task.Task{
			ID: id, Type: task.TypeOperation, Operation: operation.Name("diagnostic.echo"),
			TargetType: "switch", TargetID: "device-1", Status: status,
			ExecutionMode: operation.ExecutionModeAsync, Payload: json.RawMessage(`{"message":"hello"}`),
			CreatedBy: schedulerUserID, CreatedAt: createdAt, Version: 1,
		}}
		if status == task.StatusRunning {
			started := createdAt.Add(time.Second)
			value.StartedAt = &started
		}
		created, createErr := repository.Create(ctx, value)
		if createErr != nil {
			t.Fatalf("create task %s: %v", id, createErr)
		}
		return created
	}

	create(schedulerSuccessTaskID, task.StatusPending)
	create(schedulerCancelTaskID, task.StatusPending)
	create(schedulerStaleTaskID, task.StatusRunning)

	cancelStarted := make(chan struct{})
	shutdownStarted := make(chan struct{})
	handler := scheduler.HandlerFunc(func(ctx context.Context, value task.Persisted) (scheduler.ExecutionResult, error) {
		switch value.ID {
		case schedulerCancelTaskID:
			close(cancelStarted)
			<-ctx.Done()
			return scheduler.ExecutionResult{}, ctx.Err()
		case schedulerShutdownTaskID:
			close(shutdownStarted)
			<-ctx.Done()
			return scheduler.ExecutionResult{}, ctx.Err()
		}
		return scheduler.ExecutionResult{Status: task.StatusSuccess, Result: json.RawMessage(`{"ok":true}`)}, nil
	})
	service, err := scheduler.New(repository, handler, scheduler.Config{Workers: 2, PollInterval: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	runCtx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(runCtx) }()

	select {
	case <-cancelStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("cancel task did not start")
	}
	if _, err := service.Cancel(context.Background(), schedulerCancelTaskID); err != nil {
		t.Fatal(err)
	}
	waitPostgresTaskStatus(t, repository, schedulerSuccessTaskID, task.StatusSuccess)
	waitPostgresTaskStatus(t, repository, schedulerCancelTaskID, task.StatusCancelled)
	waitPostgresTaskStatus(t, repository, schedulerStaleTaskID, task.StatusInterrupted)
	create(schedulerShutdownTaskID, task.StatusPending)
	if _, err := service.Queue(context.Background(), schedulerShutdownTaskID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-shutdownStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown task did not start")
	}
	stop()
	if err := <-done; err != nil {
		t.Fatalf("Scheduler.Run() error = %v", err)
	}
	waitPostgresTaskStatus(t, repository, schedulerShutdownTaskID, task.StatusInterrupted)

	retry, err := service.Retry(context.Background(), scheduler.RetryRequest{
		SourceTaskID: schedulerStaleTaskID, NewTaskID: schedulerRetryTaskID,
		CreatedBy: schedulerUserID, CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if retry.Status != task.StatusQueued || retry.RetryOf != schedulerStaleTaskID || retry.ID != schedulerRetryTaskID {
		t.Fatalf("retry task = %+v", retry)
	}
	if _, err := repository.RequestCancel(ctx, retry.ID, time.Now().UTC()); err != nil {
		t.Fatalf("cancel retry task before race: %v", err)
	}

	race := create(schedulerRaceTaskID, task.StatusPending)
	if _, err := repository.QueuePending(ctx, race.ID); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	claimResult := make(chan error, 1)
	cancelResult := make(chan error, 1)
	go func() {
		<-start
		_, claimErr := repository.ClaimNextQueued(context.Background(), time.Now().UTC())
		claimResult <- claimErr
	}()
	go func() {
		<-start
		_, cancelErr := repository.RequestCancel(context.Background(), race.ID, time.Now().UTC())
		cancelResult <- cancelErr
	}()
	close(start)
	claimErr, cancelErr := <-claimResult, <-cancelResult
	if cancelErr != nil {
		t.Fatalf("RequestCancel() error = %v", cancelErr)
	}
	if claimErr != nil && !errors.Is(claimErr, task.ErrNoQueuedTask) {
		t.Fatalf("ClaimNextQueued() error = %v", claimErr)
	}
	final, err := repository.Get(ctx, race.ID)
	if err != nil {
		t.Fatal(err)
	}
	switch final.Status {
	case task.StatusCancelled:
		if final.FinishedAt == nil || final.CancelRequestedAt == nil {
			t.Fatalf("cancelled race task = %+v", final)
		}
	case task.StatusRunning:
		if final.CancelRequestedAt == nil {
			t.Fatalf("running race task lost cancellation request: %+v", final)
		}
	default:
		t.Fatalf("unexpected race status %s", final.Status)
	}
	if _, err := repository.QueuePending(ctx, race.ID); !apperror.IsCode(err, apperror.CodeStateConflict) {
		t.Fatalf("QueuePending terminal/running task error = %v", err)
	}
}

func waitPostgresTaskStatus(t *testing.T, repository *TaskRepository, id string, want task.Status) task.Persisted {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		value, err := repository.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if value.Status == want {
			return value
		}
		if time.Now().After(deadline) {
			t.Fatalf("task %s status = %s, want %s", id, value.Status, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
