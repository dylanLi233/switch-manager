package task

import (
	"testing"
	"time"
)

func TestRunningTaskCanTransitionToCancelled(t *testing.T) {
	t.Parallel()
	value := baseTask()
	queuedAt := value.CreatedAt.Add(time.Second)
	if err := value.Transition(StatusQueued, queuedAt); err != nil {
		t.Fatal(err)
	}
	startedAt := queuedAt.Add(time.Second)
	if err := value.Transition(StatusRunning, startedAt); err != nil {
		t.Fatal(err)
	}
	cancelledAt := startedAt.Add(time.Second)
	value.CancelRequestedAt = &cancelledAt
	if err := value.Transition(StatusCancelled, cancelledAt); err != nil {
		t.Fatal(err)
	}
	if err := value.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestTaskRejectsCancellationAfterFinish(t *testing.T) {
	t.Parallel()
	value := baseTask()
	startedAt := value.CreatedAt.Add(time.Second)
	finishedAt := startedAt.Add(time.Second)
	cancelledAt := finishedAt.Add(time.Second)
	value.Status = StatusSuccess
	value.StartedAt = &startedAt
	value.FinishedAt = &finishedAt
	value.CancelRequestedAt = &cancelledAt
	if err := value.Validate(); err == nil {
		t.Fatal("expected cancellation timestamp after finish to fail")
	}
}
