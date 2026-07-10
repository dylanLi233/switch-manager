// Package operation defines vendor-neutral switch operations and execution plans.
package operation

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dylanLi233/switch-manager/internal/domain/auth"
	"github.com/dylanLi233/switch-manager/internal/domain/device"
)

// Name is a stable vendor-neutral operation identifier such as vlan.create.
type Name string

// ExecutionMode selects whether an API caller waits for task completion.
type ExecutionMode string

const (
	ExecutionModeSync  ExecutionMode = "SYNC"
	ExecutionModeAsync ExecutionMode = "ASYNC"
)

// Validate reports whether the execution mode is supported.
func (m ExecutionMode) Validate() error {
	switch m {
	case ExecutionModeSync, ExecutionModeAsync:
		return nil
	default:
		return fmt.Errorf("unsupported execution mode %q", m)
	}
}

// Class identifies the side-effect category of an operation.
type Class string

const (
	ClassQuery   Class = "QUERY"
	ClassConfig  Class = "CONFIG"
	ClassBackup  Class = "BACKUP"
	ClassRestore Class = "RESTORE"
)

// Validate reports whether the operation class is supported.
func (c Class) Validate() error {
	switch c {
	case ClassQuery, ClassConfig, ClassBackup, ClassRestore:
		return nil
	default:
		return fmt.Errorf("unsupported operation class %q", c)
	}
}

// RiskLevel records the command risk decision produced by a plugin and policy engine.
type RiskLevel string

const (
	RiskLow     RiskLevel = "LOW"
	RiskMedium  RiskLevel = "MEDIUM"
	RiskHigh    RiskLevel = "HIGH"
	RiskBlocked RiskLevel = "BLOCKED"
)

// Validate reports whether the risk level is supported.
func (r RiskLevel) Validate() error {
	switch r {
	case RiskLow, RiskMedium, RiskHigh, RiskBlocked:
		return nil
	default:
		return fmt.Errorf("unsupported risk level %q", r)
	}
}

// Request is a vendor-neutral request accepted by the application layer.
type Request struct {
	Name           Name
	Class          Class
	DeviceID       string
	Parameters     map[string]any
	ExecutionMode  ExecutionMode
	DryRun         bool
	SaveConfig     bool
	ConfirmRisk    bool
	IdempotencyKey string
	Actor          auth.Actor
}

// Validate enforces operation request invariants.
func (r Request) Validate() error {
	if strings.TrimSpace(string(r.Name)) == "" {
		return errors.New("operation name is required")
	}
	if strings.TrimSpace(r.DeviceID) == "" {
		return errors.New("operation device ID is required")
	}
	if err := r.Class.Validate(); err != nil {
		return err
	}
	if err := r.ExecutionMode.Validate(); err != nil {
		return err
	}
	if err := r.Actor.Validate(); err != nil {
		return fmt.Errorf("validate actor: %w", err)
	}
	if r.SaveConfig && r.Class != ClassConfig {
		return errors.New("save_config is only valid for configuration operations")
	}
	if (r.Class == ClassBackup || r.Class == ClassRestore) && r.ExecutionMode != ExecutionModeAsync {
		return fmt.Errorf("%s operations must be asynchronous", r.Class)
	}
	return nil
}

// PlannedCommand is one immutable CLI command in an execution plan.
type PlannedCommand struct {
	Sequence     int
	Text         string
	Sensitive    bool
	ExpectedMode string
	Timeout      time.Duration
}

// ExecutionPlan is a plugin-generated immutable command plan.
type ExecutionPlan struct {
	PlanID          string
	DeviceID        string
	Vendor          device.Vendor
	PluginName      string
	PluginVersion   string
	Operation       Name
	Class           Class
	Commands        []PlannedCommand
	EnterConfigMode bool
	SaveConfig      bool
	RiskLevel       RiskLevel
	Warnings        []string
}

// Validate enforces execution plan invariants before dry-run output or execution.
func (p ExecutionPlan) Validate() error {
	if strings.TrimSpace(p.PlanID) == "" {
		return errors.New("plan ID is required")
	}
	if strings.TrimSpace(p.DeviceID) == "" {
		return errors.New("plan device ID is required")
	}
	if err := p.Vendor.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(p.PluginName) == "" || strings.TrimSpace(p.PluginVersion) == "" {
		return errors.New("plugin name and version are required")
	}
	if strings.TrimSpace(string(p.Operation)) == "" {
		return errors.New("plan operation is required")
	}
	if err := p.Class.Validate(); err != nil {
		return err
	}
	if err := p.RiskLevel.Validate(); err != nil {
		return err
	}
	if p.SaveConfig && p.Class != ClassConfig {
		return errors.New("plan save_config is only valid for configuration operations")
	}
	if len(p.Commands) == 0 {
		return errors.New("execution plan must contain at least one command")
	}
	for i, command := range p.Commands {
		wantSequence := i + 1
		if command.Sequence != wantSequence {
			return fmt.Errorf("command sequence %d must be %d", command.Sequence, wantSequence)
		}
		if strings.TrimSpace(command.Text) == "" {
			return fmt.Errorf("command %d text is required", command.Sequence)
		}
		if command.Timeout <= 0 {
			return fmt.Errorf("command %d timeout must be positive", command.Sequence)
		}
	}
	return nil
}

// ResultStatus is the final result of one operation execution.
type ResultStatus string

const (
	ResultSuccess        ResultStatus = "SUCCESS"
	ResultPartialSuccess ResultStatus = "PARTIAL_SUCCESS"
	ResultFailed         ResultStatus = "FAILED"
)

// Validate reports whether the result status is supported.
func (s ResultStatus) Validate() error {
	switch s {
	case ResultSuccess, ResultPartialSuccess, ResultFailed:
		return nil
	default:
		return fmt.Errorf("unsupported result status %q", s)
	}
}

// CommandExecution records one command's normalized execution metadata.
type CommandExecution struct {
	Sequence        int
	Succeeded       bool
	OutputTruncated bool
	ErrorCode       string
	Duration        time.Duration
}

// Result is the normalized outcome returned by plugins to the core service.
type Result struct {
	Status       ResultStatus
	Data         any
	Commands     []CommandExecution
	ErrorCode    string
	ErrorMessage string
	StartedAt    time.Time
	FinishedAt   time.Time
}

// Validate enforces internally consistent success and failure outcomes.
func (r Result) Validate() error {
	if err := r.Status.Validate(); err != nil {
		return err
	}
	if r.StartedAt.IsZero() || r.FinishedAt.IsZero() {
		return errors.New("result start and finish times are required")
	}
	if r.FinishedAt.Before(r.StartedAt) {
		return errors.New("result finish time cannot precede start time")
	}
	switch r.Status {
	case ResultSuccess:
		if r.ErrorCode != "" || r.ErrorMessage != "" {
			return errors.New("successful result cannot contain an error")
		}
	case ResultFailed:
		if strings.TrimSpace(r.ErrorCode) == "" {
			return errors.New("failed result requires an error code")
		}
	case ResultPartialSuccess:
		if strings.TrimSpace(r.ErrorCode) == "" {
			return errors.New("partially successful result requires an error code")
		}
		if len(r.Commands) == 0 {
			return errors.New("partially successful result requires command outcomes")
		}
	}
	for i, command := range r.Commands {
		if command.Sequence != i+1 {
			return fmt.Errorf("command execution sequence %d must be %d", command.Sequence, i+1)
		}
		if command.Duration < 0 {
			return fmt.Errorf("command execution %d duration cannot be negative", command.Sequence)
		}
		if command.Succeeded && command.ErrorCode != "" {
			return fmt.Errorf("successful command execution %d cannot contain an error code", command.Sequence)
		}
		if !command.Succeeded && strings.TrimSpace(command.ErrorCode) == "" {
			return fmt.Errorf("failed command execution %d requires an error code", command.Sequence)
		}
	}
	return nil
}
