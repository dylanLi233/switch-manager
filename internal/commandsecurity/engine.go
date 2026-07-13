// Package commandsecurity evaluates plugin-supplied custom command rules in
// the trusted core before a task or execution plan can be accepted.
package commandsecurity

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
)

// CommandDecision records the matched rules and effective risk for one command.
type CommandDecision struct {
	Text       string
	RiskLevel  pluginapi.RiskLevel
	RuleIDs    []string
	Warnings   []string
}

// Decision is the complete immutable outcome used to verify a plugin plan and
// enforce runtime output and timeout limits.
type Decision struct {
	Operation        pluginapi.OperationName
	Class            pluginapi.OperationClass
	Commands         []CommandDecision
	RiskLevel        pluginapi.RiskLevel
	Limits           pluginapi.CustomCommandLimits
	OutputRedactions []pluginapi.OutputRedaction
}

func (d Decision) CommandTexts() []string {
	result := make([]string, len(d.Commands))
	for index, command := range d.Commands {
		result[index] = command.Text
	}
	return result
}

func (d Decision) Warnings() []string {
	result := make([]string, 0)
	seen := make(map[string]struct{})
	for _, command := range d.Commands {
		for _, warning := range command.Warnings {
			if warning == "" {
				continue
			}
			if _, exists := seen[warning]; exists {
				continue
			}
			seen[warning] = struct{}{}
			result = append(result, warning)
		}
	}
	return result
}

// Engine is stateless and safe for concurrent use.
type Engine struct{}

func New() *Engine { return &Engine{} }

// Evaluate rejects malformed input before rule matching. A command is allowed
// only if at least one non-blocking rule matches, and any matching BLOCKED rule
// wins over every allow rule.
func (e *Engine) Evaluate(policy pluginapi.CustomCommandPolicy, operation pluginapi.OperationName, parameters map[string]any) (Decision, error) {
	if e == nil {
		return Decision{}, errors.New("custom command security engine is nil")
	}
	if err := policy.Validate(); err != nil {
		return Decision{}, apperror.Wrap(apperror.CodeInternalError, "", err)
	}
	if policy.Operation != operation {
		return Decision{}, apperror.New(apperror.CodeInternalError, "custom command policy operation mismatch")
	}
	commands, err := pluginapi.DecodeCustomCommands(parameters)
	if err != nil {
		return Decision{}, apperror.Wrap(apperror.CodeValidationError, "", err)
	}
	if len(commands) == 0 {
		return Decision{}, apperror.New(apperror.CodeValidationError, "at least one command is required")
	}
	if len(commands) > policy.Limits.MaxCommands {
		return Decision{}, apperror.New(apperror.CodeValidationError, "custom command count exceeds the configured limit")
	}

	decision := Decision{
		Operation: operation, Class: policy.Class, RiskLevel: pluginapi.RiskLow,
		Limits: policy.Limits,
		OutputRedactions: append([]pluginapi.OutputRedaction(nil), policy.OutputRedactions...),
		Commands: make([]CommandDecision, 0, len(commands)),
	}
	totalBytes := 0
	for index, command := range commands {
		if err := validateCommandShape(command, policy.Limits.MaxCommandBytes); err != nil {
			return Decision{}, apperror.Wrap(apperror.CodeValidationError, fmt.Sprintf("command %d is invalid", index+1), err)
		}
		totalBytes += len(command)
		if totalBytes > policy.Limits.MaxTotalBytes {
			return Decision{}, apperror.New(apperror.CodeValidationError, "custom command total length exceeds the configured limit")
		}
		commandDecision, err := evaluateCommand(policy.Rules, command)
		if err != nil {
			return Decision{}, err
		}
		decision.Commands = append(decision.Commands, commandDecision)
		decision.RiskLevel = maxRisk(decision.RiskLevel, commandDecision.RiskLevel)
	}
	return decision, nil
}

func validateCommandShape(command string, maxBytes int) error {
	if command == "" {
		return errors.New("command is empty")
	}
	if !utf8.ValidString(command) {
		return errors.New("command is not valid UTF-8")
	}
	if command != strings.TrimSpace(command) {
		return errors.New("command must not contain leading or trailing whitespace")
	}
	if len(command) > maxBytes {
		return fmt.Errorf("command exceeds %d bytes", maxBytes)
	}
	if strings.ContainsAny(command, "\r\n\x00") {
		return errors.New("command contains a forbidden control character")
	}
	for _, character := range command {
		if character < 0x20 || character == 0x7f {
			return errors.New("command contains a forbidden control character")
		}
	}
	return nil
}

func evaluateCommand(rules []pluginapi.CustomCommandRule, command string) (CommandDecision, error) {
	matchedAllow := false
	blocked := false
	risk := pluginapi.RiskLow
	ruleIDs := make([]string, 0)
	warnings := make([]string, 0)
	for _, rule := range rules {
		if !matches(rule, command) {
			continue
		}
		ruleIDs = append(ruleIDs, rule.ID)
		if rule.Warning != "" {
			warnings = append(warnings, rule.Warning)
		}
		switch rule.Effect {
		case pluginapi.CommandBlocked:
			blocked = true
		case pluginapi.CommandAllowLow:
			matchedAllow = true
		case pluginapi.CommandAllowMedium:
			matchedAllow = true
			risk = maxRisk(risk, pluginapi.RiskMedium)
		case pluginapi.CommandAllowHigh:
			matchedAllow = true
			risk = maxRisk(risk, pluginapi.RiskHigh)
		}
	}
	if blocked || !matchedAllow {
		return CommandDecision{}, apperror.New(apperror.CodeDangerousCommandBlocked, "custom command is blocked by security policy")
	}
	return CommandDecision{Text: command, RiskLevel: risk, RuleIDs: ruleIDs, Warnings: warnings}, nil
}

func matches(rule pluginapi.CustomCommandRule, command string) bool {
	switch rule.Match {
	case pluginapi.CommandMatchExact:
		return command == rule.Pattern
	case pluginapi.CommandMatchPrefix:
		return strings.HasPrefix(command, rule.Pattern)
	default:
		return false
	}
}

func maxRisk(left, right pluginapi.RiskLevel) pluginapi.RiskLevel {
	if riskRank(right) > riskRank(left) {
		return right
	}
	return left
}

func riskRank(level pluginapi.RiskLevel) int {
	switch level {
	case pluginapi.RiskBlocked:
		return 4
	case pluginapi.RiskHigh:
		return 3
	case pluginapi.RiskMedium:
		return 2
	case pluginapi.RiskLow:
		return 1
	default:
		return 0
	}
}

// RedactTranscript applies literal replacements before plugin parsing. The
// original transcript remains unchanged.
func RedactTranscript(transcript pluginapi.Transcript, redactions []pluginapi.OutputRedaction) pluginapi.Transcript {
	clone := transcript
	clone.Commands = append([]pluginapi.CommandRecord(nil), transcript.Commands...)
	for index := range clone.Commands {
		output := clone.Commands[index].Output
		for _, redaction := range redactions {
			output = strings.ReplaceAll(output, redaction.Literal, redaction.Replacement)
		}
		clone.Commands[index].Output = output
	}
	return clone
}
