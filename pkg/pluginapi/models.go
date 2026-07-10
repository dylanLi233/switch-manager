package pluginapi

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Vendor is a stable vendor identifier shared by the SDK and core adapters.
type Vendor string

const (
	VendorHuawei Vendor = "HUAWEI"
	VendorH3C    Vendor = "H3C"
)

func (v Vendor) Validate() error {
	switch v {
	case VendorHuawei, VendorH3C:
		return nil
	default:
		return fmt.Errorf("unsupported vendor %q", v)
	}
}

// OperationName is a stable dotted operation identifier.
type OperationName string

// OperationClass describes whether an operation is read-only or mutating.
type OperationClass string

const (
	ClassQuery   OperationClass = "QUERY"
	ClassConfig  OperationClass = "CONFIG"
	ClassBackup  OperationClass = "BACKUP"
	ClassRestore OperationClass = "RESTORE"
)

func (c OperationClass) Validate() error {
	switch c {
	case ClassQuery, ClassConfig, ClassBackup, ClassRestore:
		return nil
	default:
		return fmt.Errorf("unsupported operation class %q", c)
	}
}

// RiskLevel is the plugin's normalized command risk decision.
type RiskLevel string

const (
	RiskLow     RiskLevel = "LOW"
	RiskMedium  RiskLevel = "MEDIUM"
	RiskHigh    RiskLevel = "HIGH"
	RiskBlocked RiskLevel = "BLOCKED"
)

func (r RiskLevel) Validate() error {
	switch r {
	case RiskLow, RiskMedium, RiskHigh, RiskBlocked:
		return nil
	default:
		return fmt.Errorf("unsupported risk level %q", r)
	}
}

// PlannedCommand is an immutable command emitted by a plugin.
type PlannedCommand struct {
	Sequence     int
	Text         string
	Sensitive    bool
	ExpectedMode string
	Timeout      time.Duration
}

// ExecutionPlan is the SDK representation converted by the core before use.
type ExecutionPlan struct {
	PlanID          string
	DeviceID        string
	Vendor          Vendor
	PluginName      string
	PluginVersion   string
	Operation       OperationName
	Class           OperationClass
	Commands        []PlannedCommand
	EnterConfigMode bool
	SaveConfig      bool
	RiskLevel       RiskLevel
	Warnings        []string
}

func (p ExecutionPlan) Validate() error {
	if strings.TrimSpace(p.PlanID) == "" || strings.TrimSpace(p.DeviceID) == "" {
		return errors.New("plan and device IDs are required")
	}
	if err := p.Vendor.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(p.PluginName) == "" || strings.TrimSpace(p.PluginVersion) == "" {
		return errors.New("plugin name and version are required")
	}
	if err := ValidateOperationName(p.Operation); err != nil {
		return err
	}
	if err := p.Class.Validate(); err != nil {
		return err
	}
	if err := p.RiskLevel.Validate(); err != nil {
		return err
	}
	if p.SaveConfig && p.Class != ClassConfig {
		return errors.New("save_config is only valid for configuration plans")
	}
	if len(p.Commands) == 0 {
		return errors.New("execution plan must contain at least one command")
	}
	for index, command := range p.Commands {
		if command.Sequence != index+1 {
			return fmt.Errorf("command sequence %d must be %d", command.Sequence, index+1)
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

// ResultStatus is a normalized plugin parse result.
type ResultStatus string

const (
	ResultSuccess        ResultStatus = "SUCCESS"
	ResultPartialSuccess ResultStatus = "PARTIAL_SUCCESS"
	ResultFailed         ResultStatus = "FAILED"
)

func (s ResultStatus) Validate() error {
	switch s {
	case ResultSuccess, ResultPartialSuccess, ResultFailed:
		return nil
	default:
		return fmt.Errorf("unsupported result status %q", s)
	}
}

// CommandExecution is one normalized command outcome.
type CommandExecution struct {
	Sequence        int
	Succeeded       bool
	OutputTruncated bool
	ErrorCode       string
	Duration        time.Duration
}

// OperationResult is converted to the core domain result after validation.
type OperationResult struct {
	Status       ResultStatus
	Data         any
	Commands     []CommandExecution
	ErrorCode    string
	ErrorMessage string
	StartedAt    time.Time
	FinishedAt   time.Time
}

func (r OperationResult) Validate() error {
	if err := r.Status.Validate(); err != nil {
		return err
	}
	if r.StartedAt.IsZero() || r.FinishedAt.IsZero() || r.FinishedAt.Before(r.StartedAt) {
		return errors.New("result timestamps are invalid")
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
		if strings.TrimSpace(r.ErrorCode) == "" || len(r.Commands) == 0 {
			return errors.New("partial result requires an error code and command outcomes")
		}
	}
	for index, command := range r.Commands {
		if command.Sequence != index+1 || command.Duration < 0 {
			return fmt.Errorf("command execution %d is invalid", command.Sequence)
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
