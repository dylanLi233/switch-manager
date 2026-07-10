// Package task defines durable task state and batch aggregation rules.
package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dylanLi233/switch-manager/internal/domain/operation"
)

// Type identifies a task's orchestration role.
type Type string

const (
	TypeOperation   Type = "OPERATION"
	TypeBatchParent Type = "BATCH_PARENT"
	TypeBatchChild  Type = "BATCH_CHILD"
)

// Validate reports whether the task type is supported.
func (t Type) Validate() error {
	switch t {
	case TypeOperation, TypeBatchParent, TypeBatchChild:
		return nil
	default:
		return fmt.Errorf("unsupported task type %q", t)
	}
}

// Status is the persisted task lifecycle state.
type Status string

const (
	StatusPending        Status = "PENDING"
	StatusQueued         Status = "QUEUED"
	StatusRunning        Status = "RUNNING"
	StatusSuccess        Status = "SUCCESS"
	StatusPartialSuccess Status = "PARTIAL_SUCCESS"
	StatusFailed         Status = "FAILED"
	StatusCancelled      Status = "CANCELLED"
	StatusInterrupted    Status = "INTERRUPTED"
)

// Validate reports whether the task status is known.
func (s Status) Validate() error {
	switch s {
	case StatusPending, StatusQueued, StatusRunning, StatusSuccess, StatusPartialSuccess,
		StatusFailed, StatusCancelled, StatusInterrupted:
		return nil
	default:
		return fmt.Errorf("unsupported task status %q", s)
	}
}

// Terminal reports whether no further state transition is permitted.
func (s Status) Terminal() bool {
	switch s {
	case StatusSuccess, StatusPartialSuccess, StatusFailed, StatusCancelled, StatusInterrupted:
		return true
	default:
		return false
	}
}

var allowedTransitions = map[Status]map[Status]struct{}{
	StatusPending: {
		StatusQueued:    {},
		StatusCancelled: {},
	},
	StatusQueued: {
		StatusRunning:   {},
		StatusCancelled: {},
	},
	StatusRunning: {
		StatusSuccess:        {},
		StatusPartialSuccess: {},
		StatusFailed:         {},
		StatusInterrupted:    {},
	},
}

// CanTransitionTo reports whether a lifecycle transition is legal.
func (s Status) CanTransitionTo(next Status) bool {
	if s.Validate() != nil || next.Validate() != nil {
		return false
	}
	_, ok := allowedTransitions[s][next]
	return ok
}

// Task is one durable operation or batch orchestration record.
type Task struct {
	ID            string
	ParentTaskID  string
	Type          Type
	Operation     operation.Name
	TargetType    string
	TargetID      string
	Status        Status
	ExecutionMode operation.ExecutionMode
	Payload       json.RawMessage
	Result        json.RawMessage
	ErrorCode     string
	CreatedBy     string
	RetryOf       string
	PluginName    string
	PluginVersion string
	CreatedAt     time.Time
	StartedAt     *time.Time
	FinishedAt    *time.Time
	Version       int64
}

// Validate enforces task identity, state, and timestamp invariants.
func (t Task) Validate() error {
	if strings.TrimSpace(t.ID) == "" {
		return errors.New("task ID is required")
	}
	if err := t.Type.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(string(t.Operation)) == "" {
		return errors.New("task operation is required")
	}
	if strings.TrimSpace(t.TargetType) == "" || strings.TrimSpace(t.TargetID) == "" {
		return errors.New("task target type and ID are required")
	}
	if err := t.Status.Validate(); err != nil {
		return err
	}
	if err := t.ExecutionMode.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(t.CreatedBy) == "" {
		return errors.New("task creator is required")
	}
	if t.CreatedAt.IsZero() {
		return errors.New("task created time is required")
	}
	if t.Version < 1 {
		return errors.New("task version must be at least 1")
	}
	if t.StartedAt != nil && t.StartedAt.Before(t.CreatedAt) {
		return errors.New("task start time cannot precede created time")
	}
	if t.FinishedAt != nil {
		if t.StartedAt == nil {
			if t.Status != StatusCancelled {
				return errors.New("finished task requires a start time")
			}
		} else if t.FinishedAt.Before(*t.StartedAt) {
			return errors.New("task finish time cannot precede start time")
		}
	}
	if t.Status == StatusRunning && t.StartedAt == nil {
		return errors.New("running task requires a start time")
	}
	if t.Status.Terminal() && t.FinishedAt == nil {
		return errors.New("terminal task requires a finish time")
	}
	if t.Status == StatusFailed && strings.TrimSpace(t.ErrorCode) == "" {
		return errors.New("failed task requires an error code")
	}
	if t.Status == StatusSuccess && t.ErrorCode != "" {
		return errors.New("successful task cannot contain an error code")
	}
	if t.RetryOf == t.ID && t.RetryOf != "" {
		return errors.New("task cannot retry itself")
	}
	return nil
}

// Transition moves a task to a legal next state and updates lifecycle timestamps.
func (t *Task) Transition(next Status, at time.Time) error {
	if t == nil {
		return errors.New("task is nil")
	}
	if at.IsZero() {
		return errors.New("transition time is required")
	}
	if !t.Status.CanTransitionTo(next) {
		return fmt.Errorf("illegal task transition %s -> %s", t.Status, next)
	}
	if at.Before(t.CreatedAt) {
		return errors.New("transition time cannot precede task creation")
	}
	if next == StatusRunning {
		started := at
		t.StartedAt = &started
	}
	if next.Terminal() {
		if t.StartedAt == nil && next != StatusCancelled {
			return errors.New("terminal transition requires a start time")
		}
		finished := at
		t.FinishedAt = &finished
	}
	t.Status = next
	t.Version++
	return nil
}

// Batch is the aggregate state of a multi-device operation.
type Batch struct {
	ID                string
	ParentTaskID      string
	Operation         operation.Name
	TotalCount        int
	SuccessCount      int
	FailedCount       int
	CancelledCount    int
	Status            Status
	ContinueOnFailure bool
	CreatedBy         string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// Validate enforces batch counters and derived state invariants.
func (b Batch) Validate() error {
	if strings.TrimSpace(b.ID) == "" || strings.TrimSpace(b.ParentTaskID) == "" {
		return errors.New("batch ID and parent task ID are required")
	}
	if strings.TrimSpace(string(b.Operation)) == "" {
		return errors.New("batch operation is required")
	}
	if b.TotalCount < 1 {
		return errors.New("batch total count must be positive")
	}
	if b.SuccessCount < 0 || b.FailedCount < 0 || b.CancelledCount < 0 {
		return errors.New("batch counters cannot be negative")
	}
	if b.SuccessCount+b.FailedCount+b.CancelledCount > b.TotalCount {
		return errors.New("batch terminal counters cannot exceed total count")
	}
	if err := b.Status.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(b.CreatedBy) == "" {
		return errors.New("batch creator is required")
	}
	if b.CreatedAt.IsZero() || b.UpdatedAt.IsZero() || b.UpdatedAt.Before(b.CreatedAt) {
		return errors.New("batch timestamps are invalid")
	}
	return nil
}

// AggregateStatus derives a parent batch status from child task states.
func AggregateStatus(children []Status) (Status, error) {
	if len(children) == 0 {
		return "", errors.New("at least one child status is required")
	}
	var success, failed, cancelled, running, queued int
	for _, child := range children {
		if err := child.Validate(); err != nil {
			return "", err
		}
		switch child {
		case StatusSuccess:
			success++
		case StatusPartialSuccess, StatusFailed, StatusInterrupted:
			failed++
		case StatusCancelled:
			cancelled++
		case StatusRunning:
			running++
		case StatusPending, StatusQueued:
			queued++
		}
	}
	if running > 0 {
		return StatusRunning, nil
	}
	if queued > 0 {
		return StatusQueued, nil
	}
	if success == len(children) {
		return StatusSuccess, nil
	}
	if failed == len(children) {
		return StatusFailed, nil
	}
	if cancelled == len(children) {
		return StatusCancelled, nil
	}
	return StatusPartialSuccess, nil
}
