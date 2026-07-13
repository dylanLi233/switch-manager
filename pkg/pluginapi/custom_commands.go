package pluginapi

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// CustomCommandEffect is the result of one explicit command rule. BLOCKED is
// absolute and cannot be overridden by another matching rule or by an actor.
type CustomCommandEffect string

const (
	CommandAllowLow     CustomCommandEffect = "ALLOW_LOW"
	CommandAllowMedium  CustomCommandEffect = "ALLOW_MEDIUM"
	CommandAllowHigh    CustomCommandEffect = "ALLOW_HIGH"
	CommandBlocked      CustomCommandEffect = "BLOCKED"
)

func (e CustomCommandEffect) Validate() error {
	switch e {
	case CommandAllowLow, CommandAllowMedium, CommandAllowHigh, CommandBlocked:
		return nil
	default:
		return fmt.Errorf("unsupported custom command effect %q", e)
	}
}

// CustomCommandMatch describes how a rule pattern is matched. V1 deliberately
// supports only exact and prefix matching; vendor plugins should not expose
// arbitrary regular expressions as configuration input.
type CustomCommandMatch string

const (
	CommandMatchExact  CustomCommandMatch = "EXACT"
	CommandMatchPrefix CustomCommandMatch = "PREFIX"
)

func (m CustomCommandMatch) Validate() error {
	switch m {
	case CommandMatchExact, CommandMatchPrefix:
		return nil
	default:
		return fmt.Errorf("unsupported custom command match %q", m)
	}
}

// CustomCommandRule is an immutable plugin-supplied safety rule.
type CustomCommandRule struct {
	ID      string
	Match   CustomCommandMatch
	Pattern string
	Effect  CustomCommandEffect
	Warning string
}

func (r CustomCommandRule) Validate() error {
	if strings.TrimSpace(r.ID) == "" || r.ID != strings.TrimSpace(r.ID) || len(r.ID) > 128 {
		return errors.New("custom command rule ID is invalid")
	}
	if err := r.Match.Validate(); err != nil {
		return err
	}
	if r.Pattern == "" || len(r.Pattern) > 512 || strings.ContainsAny(r.Pattern, "\r\n\x00") || strings.TrimLeftFunc(r.Pattern, unicode.IsSpace) != r.Pattern {
		return errors.New("custom command rule pattern is invalid")
	}
	for _, character := range r.Pattern {
		if unicode.IsControl(character) {
			return errors.New("custom command rule pattern contains a control character")
		}
	}
	if r.Match == CommandMatchExact && r.Pattern != strings.TrimSpace(r.Pattern) {
		return errors.New("exact custom command rule cannot contain trailing whitespace")
	}
	return r.Effect.Validate()
}

// OutputRedaction replaces a literal sensitive value before plugin parsing and
// before the result can be returned or persisted.
type OutputRedaction struct {
	Literal     string
	Replacement string
}

func (r OutputRedaction) Validate() error {
	if r.Literal == "" || len(r.Literal) > 512 || strings.ContainsAny(r.Literal, "\r\n\x00") {
		return errors.New("output redaction literal is invalid")
	}
	if r.Replacement == "" || len(r.Replacement) > 128 || strings.ContainsAny(r.Replacement, "\r\n\x00") {
		return errors.New("output redaction replacement is invalid")
	}
	return nil
}

// CustomCommandLimits are server-enforced upper bounds supplied by a compiled
// vendor plugin. API clients cannot raise these limits.
type CustomCommandLimits struct {
	MaxCommands        int
	MaxCommandBytes    int
	MaxTotalBytes      int
	MaxCommandTimeout  time.Duration
	MaxTotalTimeout    time.Duration
	MaxOutputBytes     int
}

func (l CustomCommandLimits) Validate() error {
	if l.MaxCommands < 1 || l.MaxCommands > 64 {
		return errors.New("custom command count limit must be between 1 and 64")
	}
	if l.MaxCommandBytes < 1 || l.MaxCommandBytes > 4096 {
		return errors.New("custom command length limit must be between 1 and 4096 bytes")
	}
	if l.MaxTotalBytes < l.MaxCommandBytes || l.MaxTotalBytes > 64*4096 {
		return errors.New("custom command total byte limit is invalid")
	}
	if l.MaxCommandTimeout <= 0 || l.MaxCommandTimeout > 2*time.Minute {
		return errors.New("custom command timeout limit is invalid")
	}
	if l.MaxTotalTimeout < l.MaxCommandTimeout || l.MaxTotalTimeout > 10*time.Minute {
		return errors.New("custom command total timeout limit is invalid")
	}
	if l.MaxOutputBytes < 1 || l.MaxOutputBytes > 16<<20 {
		return errors.New("custom command output limit must be between 1 byte and 16 MiB")
	}
	return nil
}

// CustomCommandPolicy is the complete rule set for one operation on a detected
// device. No rule match means deny.
type CustomCommandPolicy struct {
	Operation        OperationName
	Class            OperationClass
	Limits           CustomCommandLimits
	Rules            []CustomCommandRule
	OutputRedactions []OutputRedaction
}

func (p CustomCommandPolicy) Validate() error {
	if p.Operation != OperationCommandExecuteReadonly && p.Operation != OperationCommandExecuteConfig {
		return errors.New("custom command policy operation is invalid")
	}
	wantClass := ClassQuery
	if p.Operation == OperationCommandExecuteConfig {
		wantClass = ClassConfig
	}
	if p.Class != wantClass {
		return errors.New("custom command policy class does not match operation")
	}
	if err := p.Limits.Validate(); err != nil {
		return err
	}
	if len(p.Rules) == 0 || len(p.Rules) > 256 {
		return errors.New("custom command policy must contain 1-256 rules")
	}
	seen := make(map[string]struct{}, len(p.Rules))
	for _, rule := range p.Rules {
		if err := rule.Validate(); err != nil {
			return err
		}
		if _, exists := seen[rule.ID]; exists {
			return fmt.Errorf("duplicate custom command rule %q", rule.ID)
		}
		seen[rule.ID] = struct{}{}
	}
	for _, redaction := range p.OutputRedactions {
		if err := redaction.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// CustomCommandPolicyProvider is an optional plugin extension. Custom command
// operations are unsupported unless the selected plugin implements it.
type CustomCommandPolicyProvider interface {
	CustomCommandPolicy(context.Context, DeviceInfo, OperationName) (CustomCommandPolicy, error)
}

// DecodeCustomCommands accepts the JSON-compatible shape used by durable task
// payloads and returns an independent slice.
func DecodeCustomCommands(parameters map[string]any) ([]string, error) {
	if len(parameters) != 1 {
		return nil, errors.New("commands is the only supported custom command parameter")
	}
	raw, exists := parameters["commands"]
	if !exists {
		return nil, errors.New("commands is required")
	}
	switch values := raw.(type) {
	case []string:
		return append([]string(nil), values...), nil
	case []any:
		result := make([]string, len(values))
		for index, item := range values {
			value, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("command %d must be a string", index+1)
			}
			result[index] = value
		}
		return result, nil
	default:
		return nil, errors.New("commands must be an array of strings")
	}
}
