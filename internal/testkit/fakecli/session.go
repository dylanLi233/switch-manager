// Package fakecli provides a deterministic in-memory CLI session for tests.
// It never opens a network connection and contains no real vendor commands.
package fakecli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
)

const defaultMaxOutputBytes = 1 << 20

// ErrorKind identifies a stable fake-session failure category.
type ErrorKind string

const (
	ErrorInvalidScript     ErrorKind = "INVALID_SCRIPT"
	ErrorInvalidCommand    ErrorKind = "INVALID_COMMAND"
	ErrorUnexpectedCommand ErrorKind = "UNEXPECTED_COMMAND"
	ErrorScriptExhausted   ErrorKind = "SCRIPT_EXHAUSTED"
	ErrorScriptIncomplete  ErrorKind = "SCRIPT_INCOMPLETE"
	ErrorConcurrentUse     ErrorKind = "CONCURRENT_USE"
	ErrorSessionClosed     ErrorKind = "SESSION_CLOSED"
	ErrorCommandTimeout    ErrorKind = "COMMAND_TIMEOUT"
	ErrorInjectedFailure   ErrorKind = "INJECTED_FAILURE"
)

// Error is a test-facing error that keeps an optional injected cause.
type Error struct {
	Kind    ErrorKind
	Message string
	cause   error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if strings.TrimSpace(e.Message) == "" {
		return string(e.Kind)
	}
	return string(e.Kind) + ": " + e.Message
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// IsKind reports whether err contains a fake-session error of kind.
func IsKind(err error, kind ErrorKind) bool {
	var target *Error
	return errors.As(err, &target) && target.Kind == kind
}

func newError(kind ErrorKind, message string, cause error) error {
	return &Error{Kind: kind, Message: strings.TrimSpace(message), cause: cause}
}

// Step is one expected interaction in a scripted session.
type Step struct {
	Expect     pluginapi.PlannedCommand
	Output     string
	Delay      time.Duration
	Failure    error
	ErrorCode  string
	Disconnect bool
}

func (s Step) validate(index int) error {
	if err := validateCommand(s.Expect); err != nil {
		return fmt.Errorf("step %d expected command: %w", index+1, err)
	}
	if s.Delay < 0 {
		return fmt.Errorf("step %d delay cannot be negative", index+1)
	}
	if s.Failure == nil && strings.TrimSpace(s.ErrorCode) != "" {
		return fmt.Errorf("step %d error code requires an injected failure", index+1)
	}
	if s.Disconnect && s.Failure != nil {
		return fmt.Errorf("step %d cannot combine disconnect and injected failure", index+1)
	}
	return nil
}

// Options controls fake-session output and time behavior.
type Options struct {
	MaxOutputBytes int
	Now            func() time.Time
}

type recordedCommand struct {
	command    pluginapi.CommandRecord
	startedAt  time.Time
	finishedAt time.Time
}

// Session implements pluginapi.CLISession with a strict ordered script.
type Session struct {
	mu             sync.Mutex
	steps          []Step
	next           int
	records        []recordedCommand
	violations     []error
	closed         bool
	maxOutputBytes int
	now            func() time.Time
	inUse          atomic.Bool
}

// New validates and copies a script. A session may contain zero steps so tests
// can explicitly verify SCRIPT_EXHAUSTED behavior.
func New(steps []Step, options Options) (*Session, error) {
	if options.MaxOutputBytes < 0 {
		return nil, newError(ErrorInvalidScript, "maximum output bytes cannot be negative", nil)
	}
	maxOutputBytes := options.MaxOutputBytes
	if maxOutputBytes == 0 {
		maxOutputBytes = defaultMaxOutputBytes
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}

	copied := append([]Step(nil), steps...)
	for index, step := range copied {
		if err := step.validate(index); err != nil {
			return nil, newError(ErrorInvalidScript, err.Error(), err)
		}
	}
	return &Session{
		steps: copied, maxOutputBytes: maxOutputBytes, now: now,
		records: make([]recordedCommand, 0, len(copied)),
	}, nil
}

// Execute consumes exactly one matching script step. Concurrent calls are
// rejected because one network CLI session is not safe for interleaved use.
func (s *Session) Execute(ctx context.Context, command pluginapi.PlannedCommand) (pluginapi.CommandOutput, error) {
	if s == nil {
		return pluginapi.CommandOutput{}, newError(ErrorSessionClosed, "session is nil", nil)
	}
	if ctx == nil {
		return pluginapi.CommandOutput{}, newError(ErrorInvalidCommand, "context is required", nil)
	}
	if err := validateCommand(command); err != nil {
		return pluginapi.CommandOutput{}, newError(ErrorInvalidCommand, err.Error(), err)
	}
	if !s.inUse.CompareAndSwap(false, true) {
		err := newError(ErrorConcurrentUse, "CLI session is already executing a command", nil)
		s.addViolation(err)
		return pluginapi.CommandOutput{}, err
	}
	defer s.inUse.Store(false)

	if err := ctx.Err(); err != nil {
		return pluginapi.CommandOutput{}, err
	}

	step, err := s.consume(command)
	if err != nil {
		return pluginapi.CommandOutput{}, err
	}
	startedAt := s.now().UTC()
	output, errorCode, executeErr := s.runStep(ctx, command, step)
	finishedAt := s.now().UTC()
	if finishedAt.Before(startedAt) {
		finishedAt = startedAt
	}
	output.Duration = finishedAt.Sub(startedAt)

	record := pluginapi.CommandRecord{
		Sequence: command.Sequence, Command: command.Text, Output: output.Output,
		Succeeded: executeErr == nil, ErrorCode: errorCode, Duration: output.Duration,
		OutputTruncated: output.OutputTruncated,
	}
	s.record(record, startedAt, finishedAt, step.Disconnect)
	return output, executeErr
}

func (s *Session) consume(command pluginapi.PlannedCommand) (Step, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Step{}, newError(ErrorSessionClosed, "CLI session is closed", nil)
	}
	if s.next >= len(s.steps) {
		err := newError(ErrorScriptExhausted, "no scripted command remains", nil)
		s.violations = append(s.violations, err)
		return Step{}, err
	}
	step := s.steps[s.next]
	if step.Expect != command {
		err := newError(
			ErrorUnexpectedCommand,
			fmt.Sprintf("step %d expected %+v but received %+v", s.next+1, step.Expect, command),
			nil,
		)
		s.violations = append(s.violations, err)
		return Step{}, err
	}
	s.next++
	return step, nil
}

func (s *Session) runStep(ctx context.Context, command pluginapi.PlannedCommand, step Step) (pluginapi.CommandOutput, string, error) {
	if step.Delay > 0 {
		delayTimer := time.NewTimer(step.Delay)
		timeoutTimer := time.NewTimer(command.Timeout)
		defer delayTimer.Stop()
		defer timeoutTimer.Stop()
		select {
		case <-delayTimer.C:
		case <-ctx.Done():
			return pluginapi.CommandOutput{}, contextErrorCode(ctx.Err()), ctx.Err()
		case <-timeoutTimer.C:
			return pluginapi.CommandOutput{}, "COMMAND_TIMEOUT", newError(ErrorCommandTimeout, "scripted command exceeded its timeout", nil)
		}
	} else if err := ctx.Err(); err != nil {
		return pluginapi.CommandOutput{}, contextErrorCode(err), err
	}

	if step.Disconnect {
		return pluginapi.CommandOutput{}, "DEVICE_UNREACHABLE", newError(ErrorSessionClosed, "scripted device disconnected", nil)
	}
	if step.Failure != nil {
		code := strings.TrimSpace(step.ErrorCode)
		if code == "" {
			code = "COMMAND_REJECTED"
		}
		return pluginapi.CommandOutput{}, code, newError(ErrorInjectedFailure, "scripted command failed", step.Failure)
	}

	data := []byte(step.Output)
	truncated := len(data) > s.maxOutputBytes
	if truncated {
		data = data[:s.maxOutputBytes]
	}
	return pluginapi.CommandOutput{Output: string(data), OutputTruncated: truncated}, "", nil
}

func contextErrorCode(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "CONTEXT_CANCELLED"
	case errors.Is(err, context.DeadlineExceeded):
		return "CONTEXT_DEADLINE_EXCEEDED"
	default:
		return "CONTEXT_ERROR"
	}
}

func validateCommand(command pluginapi.PlannedCommand) error {
	if command.Sequence < 1 {
		return errors.New("command sequence must be positive")
	}
	if strings.TrimSpace(command.Text) == "" {
		return errors.New("command text is required")
	}
	if strings.ContainsAny(command.Text, "\r\n") {
		return errors.New("command text must be a single line")
	}
	if command.Timeout <= 0 {
		return errors.New("command timeout must be positive")
	}
	return nil
}

func (s *Session) record(record pluginapi.CommandRecord, startedAt, finishedAt time.Time, disconnect bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, recordedCommand{command: record, startedAt: startedAt, finishedAt: finishedAt})
	if disconnect {
		s.closed = true
	}
}

func (s *Session) addViolation(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.violations = append(s.violations, err)
}

// Close prevents future commands. It is idempotent.
func (s *Session) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}

// Checkpoint returns the current transcript record count.
func (s *Session) Checkpoint() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.records)
}

// Records returns a defensive copy of all command records.
func (s *Session) Records() []pluginapi.CommandRecord {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]pluginapi.CommandRecord, len(s.records))
	for index, record := range s.records {
		result[index] = record.command
	}
	return result
}

// Transcript returns all consumed commands as one transcript.
func (s *Session) Transcript() pluginapi.Transcript {
	transcript, _ := s.TranscriptSince(0)
	return transcript
}

// TranscriptSince returns records created after checkpoint.
func (s *Session) TranscriptSince(checkpoint int) (pluginapi.Transcript, error) {
	if s == nil {
		return pluginapi.Transcript{}, newError(ErrorSessionClosed, "session is nil", nil)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if checkpoint < 0 || checkpoint > len(s.records) {
		return pluginapi.Transcript{}, newError(ErrorInvalidCommand, "invalid transcript checkpoint", nil)
	}
	selected := s.records[checkpoint:]
	if len(selected) == 0 {
		return pluginapi.Transcript{}, nil
	}
	commands := make([]pluginapi.CommandRecord, len(selected))
	for index, record := range selected {
		commands[index] = record.command
	}
	return pluginapi.Transcript{
		StartedAt: selected[0].startedAt,
		FinishedAt: selected[len(selected)-1].finishedAt,
		Commands: commands,
	}, nil
}

// Remaining returns the number of unconsumed script steps.
func (s *Session) Remaining() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.steps) - s.next
}

// AssertComplete verifies that every step was consumed without protocol misuse.
func (s *Session) AssertComplete() error {
	if s == nil {
		return newError(ErrorSessionClosed, "session is nil", nil)
	}
	if s.inUse.Load() {
		return newError(ErrorScriptIncomplete, "a command is still running", nil)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.violations) > 0 {
		return newError(ErrorScriptIncomplete, "session recorded protocol violations", errors.Join(s.violations...))
	}
	if remaining := len(s.steps) - s.next; remaining != 0 {
		return newError(ErrorScriptIncomplete, fmt.Sprintf("%d scripted command(s) were not consumed", remaining), nil)
	}
	return nil
}

var _ pluginapi.CLISession = (*Session)(nil)
