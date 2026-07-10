package pluginapi

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

var (
	pluginNamePattern    = regexp.MustCompile(`^[a-z][a-z0-9-]{2,63}$`)
	operationNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)
)

// Metadata is immutable plugin identity and its operation catalog.
type Metadata struct {
	Name          string
	Vendor        Vendor
	PluginVersion Version
	SDKVersion    Version
	Operations    []OperationName
}

// Validate verifies metadata against the running SDK.
func (m Metadata) Validate(runtime Version) error {
	if !pluginNamePattern.MatchString(m.Name) {
		return fmt.Errorf("plugin name %q must match %s", m.Name, pluginNamePattern)
	}
	if err := m.Vendor.Validate(); err != nil {
		return fmt.Errorf("validate plugin vendor: %w", err)
	}
	if err := m.PluginVersion.Validate(); err != nil {
		return fmt.Errorf("validate plugin version: %w", err)
	}
	if err := m.SDKVersion.Validate(); err != nil {
		return fmt.Errorf("validate plugin SDK version: %w", err)
	}
	if !runtime.CompatibleWith(m.SDKVersion) {
		return fmt.Errorf("plugin requires SDK %s but runtime is %s", m.SDKVersion, runtime)
	}
	if len(m.Operations) == 0 {
		return errors.New("plugin must declare at least one operation")
	}
	seen := make(map[OperationName]struct{}, len(m.Operations))
	for _, name := range m.Operations {
		if err := ValidateOperationName(name); err != nil {
			return err
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("duplicate plugin operation %q", name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

func (m Metadata) Clone() Metadata {
	m.Operations = append([]OperationName(nil), m.Operations...)
	return m
}

func (m Metadata) Declares(name OperationName) bool {
	for _, candidate := range m.Operations {
		if candidate == name {
			return true
		}
	}
	return false
}

func ValidateOperationName(name OperationName) error {
	if !operationNamePattern.MatchString(string(name)) {
		return fmt.Errorf("invalid operation name %q", name)
	}
	return nil
}

// DeviceInfo is the plugin-normalized identity detected from a device.
type DeviceInfo struct {
	Vendor       Vendor
	Model        string
	OSVersion    string
	PromptFamily string
	Attributes   map[string]string
}

func (d DeviceInfo) Validate() error {
	if err := d.Vendor.Validate(); err != nil {
		return err
	}
	for key := range d.Attributes {
		if strings.TrimSpace(key) == "" {
			return errors.New("device attribute key cannot be blank")
		}
	}
	return nil
}

func (d DeviceInfo) Clone() DeviceInfo {
	if d.Attributes != nil {
		attributes := make(map[string]string, len(d.Attributes))
		for key, value := range d.Attributes {
			attributes[key] = value
		}
		d.Attributes = attributes
	}
	return d
}

// SupportLevel describes support for an operation on a detected device.
type SupportLevel string

const (
	SupportSupported    SupportLevel = "SUPPORTED"
	SupportExperimental SupportLevel = "EXPERIMENTAL"
	SupportUnsupported  SupportLevel = "UNSUPPORTED"
)

func (s SupportLevel) Validate() error {
	switch s {
	case SupportSupported, SupportExperimental, SupportUnsupported:
		return nil
	default:
		return fmt.Errorf("unsupported capability level %q", s)
	}
}

// Capability is one device-specific operation decision.
type Capability struct {
	Operation OperationName
	Level     SupportLevel
	Reason    string
}

func (c Capability) Validate() error {
	if err := ValidateOperationName(c.Operation); err != nil {
		return err
	}
	if err := c.Level.Validate(); err != nil {
		return err
	}
	if c.Level == SupportUnsupported && strings.TrimSpace(c.Reason) == "" {
		return errors.New("unsupported capability requires a reason")
	}
	return nil
}

// CapabilitySet is an immutable, validated operation lookup.
type CapabilitySet struct {
	values map[OperationName]Capability
}

func NewCapabilitySet(capabilities ...Capability) (CapabilitySet, error) {
	values := make(map[OperationName]Capability, len(capabilities))
	for _, capability := range capabilities {
		if err := capability.Validate(); err != nil {
			return CapabilitySet{}, err
		}
		if _, exists := values[capability.Operation]; exists {
			return CapabilitySet{}, fmt.Errorf("duplicate capability %q", capability.Operation)
		}
		values[capability.Operation] = capability
	}
	return CapabilitySet{values: values}, nil
}

func (s CapabilitySet) Lookup(name OperationName) (Capability, bool) {
	capability, ok := s.values[name]
	return capability, ok
}

func (s CapabilitySet) All() []Capability {
	result := make([]Capability, 0, len(s.values))
	for _, capability := range s.values {
		result = append(result, capability)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Operation < result[j].Operation })
	return result
}

func (s CapabilitySet) ValidateAgainst(metadata Metadata) error {
	for _, capability := range s.values {
		if !metadata.Declares(capability.Operation) {
			return fmt.Errorf("capability %q is not declared by plugin metadata", capability.Operation)
		}
	}
	return nil
}

// PlanRequest is sanitized input. It intentionally excludes actors, RBAC,
// tasks, audit data, credentials, and database handles.
type PlanRequest struct {
	PlanID     string
	DeviceID   string
	Device     DeviceInfo
	Operation  OperationName
	Class      OperationClass
	Parameters map[string]any
	SaveConfig bool
}

func (r PlanRequest) Validate() error {
	if strings.TrimSpace(r.PlanID) == "" || strings.TrimSpace(r.DeviceID) == "" {
		return errors.New("plan and device IDs are required")
	}
	if err := r.Device.Validate(); err != nil {
		return fmt.Errorf("validate plan device: %w", err)
	}
	if err := ValidateOperationName(r.Operation); err != nil {
		return err
	}
	if err := r.Class.Validate(); err != nil {
		return err
	}
	if r.SaveConfig && r.Class != ClassConfig {
		return errors.New("save_config is only valid for configuration plans")
	}
	return nil
}

// CommandOutput is raw output from one planned command.
type CommandOutput struct {
	Output          string
	Duration        time.Duration
	OutputTruncated bool
}

// CLISession is the minimum interaction contract required by plugins.
type CLISession interface {
	Execute(context.Context, PlannedCommand) (CommandOutput, error)
}

// CommandRecord captures the core executor's transcript for plugin parsing.
type CommandRecord struct {
	Sequence        int
	Command         string
	Output          string
	Succeeded       bool
	ErrorCode       string
	Duration        time.Duration
	OutputTruncated bool
}

// Transcript is immutable input to ParseResult.
type Transcript struct {
	StartedAt  time.Time
	FinishedAt time.Time
	Commands   []CommandRecord
}

func (t Transcript) ValidateAgainst(plan ExecutionPlan) error {
	if err := plan.Validate(); err != nil {
		return fmt.Errorf("validate execution plan: %w", err)
	}
	if t.StartedAt.IsZero() || t.FinishedAt.IsZero() || t.FinishedAt.Before(t.StartedAt) {
		return errors.New("transcript timestamps are invalid")
	}
	if len(t.Commands) == 0 || len(t.Commands) > len(plan.Commands) {
		return errors.New("transcript command count is invalid")
	}
	for index, record := range t.Commands {
		planned := plan.Commands[index]
		if record.Sequence != index+1 || planned.Sequence != record.Sequence {
			return fmt.Errorf("transcript command sequence %d is invalid", record.Sequence)
		}
		if record.Command != planned.Text {
			return fmt.Errorf("transcript command %d does not match the plan", record.Sequence)
		}
		if record.Duration < 0 {
			return fmt.Errorf("transcript command %d duration cannot be negative", record.Sequence)
		}
		if record.Succeeded && record.ErrorCode != "" {
			return fmt.Errorf("successful transcript command %d cannot contain an error code", record.Sequence)
		}
		if !record.Succeeded && strings.TrimSpace(record.ErrorCode) == "" {
			return fmt.Errorf("failed transcript command %d requires an error code", record.Sequence)
		}
	}
	if len(t.Commands) < len(plan.Commands) && t.Commands[len(t.Commands)-1].Succeeded {
		return errors.New("partial transcript must end with a failed command")
	}
	return nil
}
