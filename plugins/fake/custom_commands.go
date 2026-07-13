package fake

import (
	"context"
	"fmt"
	"time"

	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
)

func (p *Plugin) CustomCommandPolicy(_ context.Context, info pluginapi.DeviceInfo, operation pluginapi.OperationName) (pluginapi.CustomCommandPolicy, error) {
	if err := info.Validate(); err != nil {
		return pluginapi.CustomCommandPolicy{}, pluginapi.WrapError(pluginapi.ErrorInvalidRequest, "device information is invalid", err)
	}
	if info.Vendor != p.vendor || info.Model != "FAKE-SW" {
		return pluginapi.CustomCommandPolicy{}, pluginapi.NewError(pluginapi.ErrorUnsupportedOperation, "custom commands require the explicit fake device fixture")
	}
	limits := pluginapi.CustomCommandLimits{
		MaxCommands: 8, MaxCommandBytes: 256, MaxTotalBytes: 1024,
		MaxCommandTimeout: 3 * time.Second, MaxTotalTimeout: 20 * time.Second,
		MaxOutputBytes: 4096,
	}
	policy := pluginapi.CustomCommandPolicy{
		Operation: operation, Limits: limits,
		OutputRedactions: []pluginapi.OutputRedaction{{Literal: "secret-token", Replacement: "[REDACTED]"}},
	}
	switch operation {
	case pluginapi.OperationCommandExecuteReadonly:
		policy.Class = pluginapi.ClassQuery
		policy.Rules = []pluginapi.CustomCommandRule{
			{ID: "fake-readonly-allow", Match: pluginapi.CommandMatchPrefix, Pattern: "fake.show ", Effect: pluginapi.CommandAllowLow},
			{ID: "fake-readonly-block-overlap", Match: pluginapi.CommandMatchPrefix, Pattern: "fake.show blocked", Effect: pluginapi.CommandBlocked, Warning: "blocked test category"},
			{ID: "fake-readonly-block-config", Match: pluginapi.CommandMatchPrefix, Pattern: "fake.set ", Effect: pluginapi.CommandBlocked},
			{ID: "fake-readonly-block-shell", Match: pluginapi.CommandMatchPrefix, Pattern: "fake.shell", Effect: pluginapi.CommandBlocked},
			{ID: "fake-readonly-block-reboot", Match: pluginapi.CommandMatchPrefix, Pattern: "fake.reboot", Effect: pluginapi.CommandBlocked},
		}
	case pluginapi.OperationCommandExecuteConfig:
		policy.Class = pluginapi.ClassConfig
		policy.Rules = []pluginapi.CustomCommandRule{
			{ID: "fake-config-allow", Match: pluginapi.CommandMatchPrefix, Pattern: "fake.set ", Effect: pluginapi.CommandAllowMedium},
			{ID: "fake-config-high", Match: pluginapi.CommandMatchPrefix, Pattern: "fake.set high-risk ", Effect: pluginapi.CommandAllowHigh, Warning: "explicit high-risk fake command"},
			{ID: "fake-config-block-overlap", Match: pluginapi.CommandMatchPrefix, Pattern: "fake.set blocked", Effect: pluginapi.CommandBlocked, Warning: "blocked test category"},
			{ID: "fake-config-block-shell", Match: pluginapi.CommandMatchPrefix, Pattern: "fake.shell", Effect: pluginapi.CommandBlocked},
			{ID: "fake-config-block-reboot", Match: pluginapi.CommandMatchPrefix, Pattern: "fake.reboot", Effect: pluginapi.CommandBlocked},
		}
	default:
		return pluginapi.CustomCommandPolicy{}, pluginapi.NewError(pluginapi.ErrorUnsupportedOperation, "operation is not a custom command")
	}
	if err := policy.Validate(); err != nil {
		return pluginapi.CustomCommandPolicy{}, pluginapi.WrapError(pluginapi.ErrorPlanInvalid, "fake custom command policy is invalid", err)
	}
	return policy, nil
}

func (p *Plugin) buildCustomCommandPlan(request pluginapi.PlanRequest) (pluginapi.ExecutionPlan, error) {
	commands, err := pluginapi.DecodeCustomCommands(request.Parameters)
	if err != nil {
		return pluginapi.ExecutionPlan{}, pluginapi.WrapError(pluginapi.ErrorInvalidRequest, "custom command parameters are invalid", err)
	}
	wantClass := pluginapi.ClassQuery
	enterConfig := false
	risk := pluginapi.RiskLow
	if request.Operation == pluginapi.OperationCommandExecuteConfig {
		wantClass, enterConfig, risk = pluginapi.ClassConfig, true, pluginapi.RiskMedium
	}
	if request.Class != wantClass {
		return pluginapi.ExecutionPlan{}, pluginapi.NewError(pluginapi.ErrorInvalidRequest, "custom command class is invalid")
	}
	planned := make([]pluginapi.PlannedCommand, len(commands))
	for index, command := range commands {
		planned[index] = pluginapi.PlannedCommand{Sequence: index + 1, Text: command, Timeout: 2 * time.Second}
	}
	metadata := p.Metadata()
	plan := pluginapi.ExecutionPlan{
		PlanID: request.PlanID, DeviceID: request.DeviceID, Vendor: p.vendor,
		PluginName: metadata.Name, PluginVersion: metadata.PluginVersion.String(),
		Operation: request.Operation, Class: request.Class, Commands: planned,
		EnterConfigMode: enterConfig, SaveConfig: request.SaveConfig, RiskLevel: risk,
	}
	if len(planned) == 0 {
		return pluginapi.ExecutionPlan{}, pluginapi.NewError(pluginapi.ErrorInvalidRequest, "at least one custom command is required")
	}
	if err := plan.Validate(); err != nil {
		return pluginapi.ExecutionPlan{}, pluginapi.WrapError(pluginapi.ErrorPlanInvalid, "fake custom command plan is invalid", err)
	}
	return plan, nil
}

func isCustomCommand(name pluginapi.OperationName) bool {
	return name == pluginapi.OperationCommandExecuteReadonly || name == pluginapi.OperationCommandExecuteConfig
}

func validateCustomCommandOutput(plan pluginapi.ExecutionPlan, data any) error {
	object, ok := data.(map[string]any)
	if !ok {
		return fmt.Errorf("custom command result must be an object")
	}
	outputs, ok := object["outputs"].([]any)
	if !ok || len(outputs) != len(plan.Commands) {
		return fmt.Errorf("custom command result outputs are invalid")
	}
	for _, output := range outputs {
		if _, ok := output.(string); !ok {
			return fmt.Errorf("custom command output must be a string")
		}
	}
	return nil
}

var _ pluginapi.CustomCommandPolicyProvider = (*Plugin)(nil)
