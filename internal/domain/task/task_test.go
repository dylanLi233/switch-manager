package task

import (
	"testing"
	"time"

	"github.com/dylanLi233/switch-manager/internal/domain/operation"
)

func baseTask() Task {
	return Task{
		ID: "task-1", Type: TypeOperation, Operation: "vlan.create",
		TargetType: "switch", TargetID: "sw-1", Status: StatusPending,
		ExecutionMode: operation.ExecutionModeSync, CreatedBy: "user-1",
		CreatedAt: time.Now().UTC(), Version: 1,
	}
}

func TestStatusTransitions(t *testing.T) {
	t.Parallel()
	legal := map[Status][]Status{
		StatusPending: {StatusQueued, StatusCancelled},
		StatusQueued:  {StatusRunning, StatusCancelled},
		StatusRunning: {StatusSuccess, StatusPartialSuccess, StatusFailed, StatusInterrupted},
	}
	for from, destinations := range legal {
		for _, to := range destinations {
			if !from.CanTransitionTo(to) {
				t.Fatalf("expected %s -> %s to be legal", from, to)
			}
		}
	}
	for _, terminal := range []Status{StatusSuccess, StatusPartialSuccess, StatusFailed, StatusCancelled, StatusInterrupted} {
		if terminal.CanTransitionTo(StatusRunning) {
			t.Fatalf("terminal status %s must not transition to RUNNING", terminal)
		}
		if !terminal.Terminal() {
			t.Fatalf("%s must be terminal", terminal)
		}
	}
}

func TestTaskTransitionLifecycle(t *testing.T) {
	t.Parallel()
	task := baseTask()
	queuedAt := task.CreatedAt.Add(time.Second)
	if err := task.Transition(StatusQueued, queuedAt); err != nil {
		t.Fatalf("queue transition error = %v", err)
	}
	startedAt := queuedAt.Add(time.Second)
	if err := task.Transition(StatusRunning, startedAt); err != nil {
		t.Fatalf("run transition error = %v", err)
	}
	finishedAt := startedAt.Add(time.Second)
	if err := task.Transition(StatusSuccess, finishedAt); err != nil {
		t.Fatalf("success transition error = %v", err)
	}
	if task.StartedAt == nil || task.FinishedAt == nil {
		t.Fatal("expected lifecycle timestamps")
	}
	if err := task.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if err := task.Transition(StatusRunning, finishedAt.Add(time.Second)); err == nil {
		t.Fatal("expected terminal transition rejection")
	}
}

func TestCancelledPendingTask(t *testing.T) {
	t.Parallel()
	task := baseTask()
	if err := task.Transition(StatusCancelled, task.CreatedAt.Add(time.Second)); err != nil {
		t.Fatalf("cancel transition error = %v", err)
	}
	if err := task.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestAggregateStatus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		children []Status
		want     Status
	}{
		{[]Status{StatusSuccess, StatusSuccess}, StatusSuccess},
		{[]Status{StatusFailed, StatusInterrupted}, StatusFailed},
		{[]Status{StatusCancelled, StatusCancelled}, StatusCancelled},
		{[]Status{StatusSuccess, StatusFailed}, StatusPartialSuccess},
		{[]Status{StatusSuccess, StatusRunning}, StatusRunning},
		{[]Status{StatusSuccess, StatusQueued}, StatusQueued},
	}
	for _, tc := range tests {
		got, err := AggregateStatus(tc.children)
		if err != nil {
			t.Fatalf("AggregateStatus(%v) error = %v", tc.children, err)
		}
		if got != tc.want {
			t.Fatalf("AggregateStatus(%v) = %s, want %s", tc.children, got, tc.want)
		}
	}
}
