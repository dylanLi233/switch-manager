package task

import (
	"context"
	"errors"
	"strings"
	"time"
)

// ErrNoQueuedTask indicates that the durable queue is currently empty.
var ErrNoQueuedTask = errors.New("no queued task")

// Persisted combines task lifecycle state with durable execution options.
type Persisted struct {
	Task
	DryRun         bool
	SaveConfig     bool
	IdempotencyKey string
}

// Validate enforces task and persistence-option invariants.
func (p Persisted) Validate() error {
	if err := p.Task.Validate(); err != nil {
		return err
	}
	if p.IdempotencyKey != "" && strings.TrimSpace(p.IdempotencyKey) == "" {
		return errors.New("idempotency key cannot be blank")
	}
	return nil
}

// Repository persists durable task state with optimistic locking.
type Repository interface {
	Create(context.Context, Persisted) (Persisted, error)
	Get(context.Context, string) (Persisted, error)
	FindByIdempotency(context.Context, string, string) (Persisted, error)
	Save(context.Context, Persisted, int64) (Persisted, error)
	ListRecoverable(context.Context, int) ([]Persisted, error)
	ClaimNextQueued(context.Context, time.Time) (Persisted, error)
	InterruptRunning(context.Context, time.Time) (int64, error)
}
