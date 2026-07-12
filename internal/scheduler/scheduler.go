// Package scheduler dispatches durable tasks to an in-process worker pool.
package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/dylanLi233/switch-manager/internal/domain/task"
)

var ErrTaskCancelled = errors.New("task cancellation requested")

type Config struct {
	Workers         int
	PollInterval    time.Duration
	RecoveryLimit   int
	FinalizeTimeout time.Duration
}

func (c Config) withDefaults() Config {
	if c.Workers <= 0 {
		c.Workers = 5
	}
	if c.PollInterval <= 0 {
		c.PollInterval = 250 * time.Millisecond
	}
	if c.RecoveryLimit <= 0 {
		c.RecoveryLimit = 1000
	}
	if c.RecoveryLimit > 1000 {
		c.RecoveryLimit = 1000
	}
	if c.FinalizeTimeout <= 0 {
		c.FinalizeTimeout = 5 * time.Second
	}
	return c
}

type ExecutionResult struct {
	Status        task.Status
	Result        json.RawMessage
	ErrorCode     string
	PluginName    string
	PluginVersion string
}

func (r ExecutionResult) Validate() error {
	switch r.Status {
	case task.StatusSuccess:
		if strings.TrimSpace(r.ErrorCode) != "" {
			return errors.New("successful result cannot contain an error code")
		}
	case task.StatusPartialSuccess, task.StatusFailed:
		if strings.TrimSpace(r.ErrorCode) == "" {
			return errors.New("failed or partial result requires an error code")
		}
	default:
		return fmt.Errorf("handler cannot directly return terminal status %q", r.Status)
	}
	if len(r.Result) > 0 {
		if !json.Valid(r.Result) {
			return errors.New("handler result must be valid JSON")
		}
		var value any
		if err := json.Unmarshal(r.Result, &value); err != nil {
			return err
		}
		switch value.(type) {
		case map[string]any, []any:
		default:
			return errors.New("handler result must be a JSON object or array")
		}
	}
	if (r.PluginName == "") != (r.PluginVersion == "") {
		return errors.New("plugin name and version must be set together")
	}
	return nil
}

type Handler interface {
	Execute(context.Context, task.Persisted) (ExecutionResult, error)
}

type HandlerFunc func(context.Context, task.Persisted) (ExecutionResult, error)

func (f HandlerFunc) Execute(ctx context.Context, value task.Persisted) (ExecutionResult, error) {
	return f(ctx, value)
}

type RetryRequest struct {
	SourceTaskID string
	NewTaskID    string
	CreatedBy    string
	CreatedAt    time.Time
}

type Scheduler struct {
	repo    task.Repository
	handler Handler
	config  Config
	now     func() time.Time

	wake    chan struct{}
	mu      sync.Mutex
	running map[string]context.CancelCauseFunc
	started atomic.Bool
}

func New(repo task.Repository, handler Handler, config Config) (*Scheduler, error) {
	if repo == nil {
		return nil, errors.New("task repository is required")
	}
	if handler == nil {
		return nil, errors.New("task handler is required")
	}
	config = config.withDefaults()
	return &Scheduler{
		repo: repo, handler: handler, config: config, now: time.Now,
		wake: make(chan struct{}, 1), running: make(map[string]context.CancelCauseFunc),
	}, nil
}

func (s *Scheduler) Recover(ctx context.Context) error {
	if ctx == nil {
		return errors.New("context is required")
	}
	now := s.now().UTC()
	if _, err := s.repo.InterruptRunning(ctx, now); err != nil {
		return fmt.Errorf("interrupt running tasks: %w", err)
	}
	recoverable, err := s.repo.ListRecoverable(ctx, s.config.RecoveryLimit)
	if err != nil {
		return fmt.Errorf("list recoverable tasks: %w", err)
	}
	for _, value := range recoverable {
		if value.Status != task.StatusPending {
			continue
		}
		if _, err := s.repo.QueuePending(ctx, value.ID); err != nil {
			return fmt.Errorf("queue recovered task %s: %w", value.ID, err)
		}
	}
	s.notify()
	return nil
}

func (s *Scheduler) Run(ctx context.Context) error {
	if ctx == nil {
		return errors.New("context is required")
	}
	if !s.started.CompareAndSwap(false, true) {
		return errors.New("scheduler is already running")
	}
	defer s.started.Store(false)
	if err := s.Recover(ctx); err != nil {
		return err
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	errCh := make(chan error, 1)
	var workers sync.WaitGroup
	for i := 0; i < s.config.Workers; i++ {
		workers.Add(1)
		go func(worker int) {
			defer workers.Done()
			if err := s.worker(runCtx); err != nil && !errors.Is(err, context.Canceled) {
				select {
				case errCh <- fmt.Errorf("worker %d: %w", worker, err):
				default:
				}
				cancel()
			}
		}(i + 1)
	}
	s.notify()

	var result error
	select {
	case <-ctx.Done():
	case result = <-errCh:
	}
	cancel()
	workers.Wait()
	return result
}

func (s *Scheduler) Queue(ctx context.Context, id string) (task.Persisted, error) {
	if ctx == nil {
		return task.Persisted{}, errors.New("context is required")
	}
	if strings.TrimSpace(id) == "" {
		return task.Persisted{}, errors.New("task ID is required")
	}
	value, err := s.repo.QueuePending(ctx, id)
	if err != nil {
		return task.Persisted{}, err
	}
	s.notify()
	return value, nil
}

func (s *Scheduler) Cancel(ctx context.Context, id string) (task.Persisted, error) {
	if ctx == nil {
		return task.Persisted{}, errors.New("context is required")
	}
	if strings.TrimSpace(id) == "" {
		return task.Persisted{}, errors.New("task ID is required")
	}
	value, err := s.repo.RequestCancel(ctx, id, s.now().UTC())
	if err != nil {
		return task.Persisted{}, err
	}
	if value.Status == task.StatusRunning {
		s.mu.Lock()
		cancel := s.running[id]
		s.mu.Unlock()
		if cancel != nil {
			cancel(ErrTaskCancelled)
		}
	}
	return value, nil
}

func (s *Scheduler) Retry(ctx context.Context, request RetryRequest) (task.Persisted, error) {
	if ctx == nil {
		return task.Persisted{}, errors.New("context is required")
	}
	if strings.TrimSpace(request.SourceTaskID) == "" || strings.TrimSpace(request.NewTaskID) == "" || strings.TrimSpace(request.CreatedBy) == "" {
		return task.Persisted{}, errors.New("source task ID, new task ID, and creator are required")
	}
	if request.SourceTaskID == request.NewTaskID {
		return task.Persisted{}, errors.New("retry task ID must differ from source task ID")
	}
	if request.CreatedAt.IsZero() {
		request.CreatedAt = s.now().UTC()
	}
	source, err := s.repo.Get(ctx, request.SourceTaskID)
	if err != nil {
		return task.Persisted{}, err
	}
	switch source.Status {
	case task.StatusFailed, task.StatusPartialSuccess, task.StatusCancelled, task.StatusInterrupted:
	default:
		return task.Persisted{}, apperror.New(apperror.CodeTaskNotCancellable, "task is not retryable")
	}

	retry := source
	retry.ID = request.NewTaskID
	retry.Status = task.StatusPending
	retry.Result = nil
	retry.ErrorCode = ""
	retry.CreatedBy = request.CreatedBy
	retry.RetryOf = source.ID
	retry.PluginName, retry.PluginVersion = "", ""
	retry.CreatedAt = request.CreatedAt.UTC()
	retry.StartedAt, retry.FinishedAt, retry.CancelRequestedAt = nil, nil, nil
	retry.Version = 1
	retry.IdempotencyKey = ""
	created, err := s.repo.Create(ctx, retry)
	if err != nil {
		return task.Persisted{}, err
	}
	queued, err := s.repo.QueuePending(ctx, created.ID)
	if err != nil {
		return created, err
	}
	s.notify()
	return queued, nil
}

func (s *Scheduler) worker(ctx context.Context) error {
	ticker := time.NewTicker(s.config.PollInterval)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		value, err := s.repo.ClaimNextQueued(ctx, s.now().UTC())
		if errors.Is(err, task.ErrNoQueuedTask) {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-s.wake:
			case <-ticker.C:
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("claim queued task: %w", err)
		}
		if err := s.execute(ctx, value); err != nil {
			return err
		}
	}
}

func (s *Scheduler) execute(parent context.Context, claimed task.Persisted) error {
	execCtx, cancel := context.WithCancelCause(parent)
	s.mu.Lock()
	s.running[claimed.ID] = cancel
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.running, claimed.ID)
		s.mu.Unlock()
		cancel(nil)
	}()

	latest := claimed
	if parent.Err() == nil {
		current, err := s.repo.Get(parent, claimed.ID)
		if err != nil {
			return fmt.Errorf("reload claimed task: %w", err)
		}
		latest = current
	}
	if latest.CancelRequestedAt != nil {
		cancel(ErrTaskCancelled)
	}

	outcome := ExecutionResult{}
	var executeErr error
	if parent.Err() != nil {
		executeErr = parent.Err()
	} else {
		outcome, executeErr = executeSafely(execCtx, s.handler, latest)
	}

	finalStatus := outcome.Status
	if executeErr != nil {
		switch {
		case errors.Is(context.Cause(execCtx), ErrTaskCancelled):
			finalStatus = task.StatusCancelled
		case parent.Err() != nil:
			finalStatus = task.StatusInterrupted
		default:
			app := apperror.Normalize(executeErr)
			finalStatus = task.StatusFailed
			outcome = ExecutionResult{Status: task.StatusFailed, ErrorCode: string(app.Code)}
		}
	} else if err := outcome.Validate(); err != nil {
		finalStatus = task.StatusFailed
		outcome = ExecutionResult{Status: task.StatusFailed, ErrorCode: string(apperror.CodeInternalError)}
	}

	finalizeCtx, finalizeCancel := context.WithTimeout(context.Background(), s.config.FinalizeTimeout)
	defer finalizeCancel()
	if err := s.finalize(finalizeCtx, latest.ID, finalStatus, outcome); err != nil {
		return fmt.Errorf("finalize task %s: %w", latest.ID, err)
	}
	return nil
}

func executeSafely(ctx context.Context, handler Handler, value task.Persisted) (result ExecutionResult, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("task handler panic: %v", recovered)
		}
	}()
	return handler.Execute(ctx, value)
}

func (s *Scheduler) finalize(ctx context.Context, id string, status task.Status, outcome ExecutionResult) error {
	for attempt := 0; attempt < 3; attempt++ {
		current, err := s.repo.Get(ctx, id)
		if err != nil {
			return err
		}
		if current.Status.Terminal() {
			return nil
		}
		if current.Status != task.StatusRunning {
			return fmt.Errorf("cannot finalize task in status %s", current.Status)
		}
		expected := current.Version
		current.Result = append(json.RawMessage(nil), outcome.Result...)
		current.PluginName, current.PluginVersion = outcome.PluginName, outcome.PluginVersion
		switch status {
		case task.StatusSuccess:
			current.ErrorCode = ""
		case task.StatusPartialSuccess, task.StatusFailed:
			current.ErrorCode = strings.TrimSpace(outcome.ErrorCode)
			if current.ErrorCode == "" {
				current.ErrorCode = string(apperror.CodeInternalError)
			}
		case task.StatusCancelled, task.StatusInterrupted:
			current.ErrorCode = ""
		default:
			return fmt.Errorf("unsupported final status %s", status)
		}
		if err := current.Task.Transition(status, s.now().UTC()); err != nil {
			return err
		}
		if _, err := s.repo.Save(ctx, current, expected); err == nil {
			return nil
		} else if !apperror.IsCode(err, apperror.CodeStateConflict) {
			return err
		}
	}
	return apperror.New(apperror.CodeStateConflict, "task finalization conflicted repeatedly")
}

func (s *Scheduler) notify() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
