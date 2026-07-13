// Package fake supplies a deterministic contract plugin for tests and early
// integration. It contains no real Huawei or H3C commands.
package fake

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
)

const (
	OperationEchoQuery  pluginapi.OperationName = "diagnostic.echo"
	OperationEchoConfig pluginapi.OperationName = "configuration.echo"
	OperationSaveConfig pluginapi.OperationName = "config.save"
)

type Plugin struct{ vendor pluginapi.Vendor }

func New(vendor pluginapi.Vendor) (*Plugin, error) {
	if err := vendor.Validate(); err != nil {
		return nil, err
	}
	return &Plugin{vendor: vendor}, nil
}

func (p *Plugin) Metadata() pluginapi.Metadata {
	return pluginapi.Metadata{
		Name:          "fake-" + strings.ToLower(string(p.vendor)),
		Vendor:        p.vendor,
		PluginVersion: pluginapi.Version{Major: 1, Minor: 0, Patch: 0},
		SDKVersion:    pluginapi.CurrentSDKVersion(),
		Operations:    []pluginapi.OperationName{OperationEchoQuery, OperationEchoConfig, OperationSaveConfig},
	}
}

func (p *Plugin) Detect(ctx context.Context, session pluginapi.CLISession) (pluginapi.DeviceInfo, error) {
	if pluginapi.IsNilCLISession(session) {
		return pluginapi.DeviceInfo{}, pluginapi.NewError(pluginapi.ErrorInvalidRequest, "CLI session is required")
	}
	output, err := session.Execute(ctx, pluginapi.PlannedCommand{Sequence: 1, Text: "fake.detect", Timeout: time.Second})
	if err != nil {
		return pluginapi.DeviceInfo{}, pluginapi.WrapError(pluginapi.ErrorDetectionFailed, "fake detection failed", err)
	}
	values := make(map[string]string)
	for _, field := range strings.Split(output.Output, ";") {
		key, value, ok := strings.Cut(field, "=")
		if !ok || strings.TrimSpace(key) == "" {
			return pluginapi.DeviceInfo{}, pluginapi.NewError(pluginapi.ErrorOutputUnparsable, "fake detection output is invalid")
		}
		values[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	if detectedVendor := values["vendor"]; detectedVendor != "" && detectedVendor != string(p.vendor) {
		return pluginapi.DeviceInfo{}, pluginapi.NewError(pluginapi.ErrorDetectionFailed, "detected vendor does not match plugin vendor")
	}
	info := pluginapi.DeviceInfo{Vendor: p.vendor, Model: values["model"], OSVersion: values["os"], PromptFamily: values["prompt"], Attributes: map[string]string{"source": "fake"}}
	if err := info.Validate(); err != nil {
		return pluginapi.DeviceInfo{}, pluginapi.WrapError(pluginapi.ErrorOutputUnparsable, "fake device information is invalid", err)
	}
	return info, nil
}

func (p *Plugin) Capabilities(_ context.Context, info pluginapi.DeviceInfo) (pluginapi.CapabilitySet, error) {
	if err := info.Validate(); err != nil {
		return pluginapi.CapabilitySet{}, pluginapi.WrapError(pluginapi.ErrorInvalidRequest, "device information is invalid", err)
	}
	if info.Vendor != p.vendor {
		return pluginapi.CapabilitySet{}, pluginapi.NewError(pluginapi.ErrorInvalidRequest, "device vendor does not match plugin")
	}
	config := pluginapi.Capability{Operation: OperationEchoConfig, Level: pluginapi.SupportUnsupported, Reason: "fake model is unknown"}
	save := pluginapi.Capability{Operation: OperationSaveConfig, Level: pluginapi.SupportUnsupported, Reason: "fake model is unknown"}
	if info.Model == "FAKE-SW" {
		config.Level, config.Reason = pluginapi.SupportSupported, ""
		save.Level, save.Reason = pluginapi.SupportSupported, ""
	}
	return pluginapi.NewCapabilitySet(pluginapi.Capability{Operation: OperationEchoQuery, Level: pluginapi.SupportSupported}, config, save)
}

func (p *Plugin) BuildPlan(ctx context.Context, request pluginapi.PlanRequest) (pluginapi.ExecutionPlan, error) {
	if err := request.Validate(); err != nil {
		return pluginapi.ExecutionPlan{}, pluginapi.WrapError(pluginapi.ErrorInvalidRequest, "plan request is invalid", err)
	}
	if request.Device.Vendor != p.vendor {
		return pluginapi.ExecutionPlan{}, pluginapi.NewError(pluginapi.ErrorInvalidRequest, "device vendor does not match plugin")
	}
	capabilities, err := p.Capabilities(ctx, request.Device)
	if err != nil {
		return pluginapi.ExecutionPlan{}, err
	}
	capability, exists := capabilities.Lookup(request.Operation)
	if !exists || capability.Level == pluginapi.SupportUnsupported {
		return pluginapi.ExecutionPlan{}, pluginapi.NewError(pluginapi.ErrorUnsupportedOperation, "operation is not supported for the fake device")
	}

	commandText, risk, enterConfig := "", pluginapi.RiskLow, false
	switch request.Operation {
	case OperationEchoQuery:
		if request.Class != pluginapi.ClassQuery {
			return pluginapi.ExecutionPlan{}, pluginapi.NewError(pluginapi.ErrorInvalidRequest, "diagnostic.echo requires QUERY class")
		}
		message, ok := request.Parameters["message"].(string)
		if !ok || strings.TrimSpace(message) == "" || len(message) > 256 || strings.ContainsAny(message, "\r\n") {
			return pluginapi.ExecutionPlan{}, pluginapi.NewError(pluginapi.ErrorInvalidRequest, "message must be a non-empty single-line string up to 256 bytes")
		}
		commandText = "fake.echo.query " + strconv.Quote(message)
	case OperationEchoConfig:
		if request.Class != pluginapi.ClassConfig {
			return pluginapi.ExecutionPlan{}, pluginapi.NewError(pluginapi.ErrorInvalidRequest, "configuration.echo requires CONFIG class")
		}
		message, ok := request.Parameters["message"].(string)
		if !ok || strings.TrimSpace(message) == "" || len(message) > 256 || strings.ContainsAny(message, "\r\n") {
			return pluginapi.ExecutionPlan{}, pluginapi.NewError(pluginapi.ErrorInvalidRequest, "message must be a non-empty single-line string up to 256 bytes")
		}
		commandText, risk, enterConfig = "fake.echo.config "+strconv.Quote(message), pluginapi.RiskMedium, true
	case OperationSaveConfig:
		if request.Class != pluginapi.ClassConfig || request.SaveConfig {
			return pluginapi.ExecutionPlan{}, pluginapi.NewError(pluginapi.ErrorInvalidRequest, "config.save requires CONFIG class and cannot recursively request save_config")
		}
		commandText, risk = "fake.config.save", pluginapi.RiskMedium
	default:
		return pluginapi.ExecutionPlan{}, pluginapi.NewError(pluginapi.ErrorUnsupportedOperation, "operation is not declared")
	}

	metadata := p.Metadata()
	plan := pluginapi.ExecutionPlan{PlanID: request.PlanID, DeviceID: request.DeviceID, Vendor: p.vendor, PluginName: metadata.Name, PluginVersion: metadata.PluginVersion.String(), Operation: request.Operation, Class: request.Class, EnterConfigMode: enterConfig, SaveConfig: request.SaveConfig, RiskLevel: risk, Commands: []pluginapi.PlannedCommand{{Sequence: 1, Text: commandText, Timeout: 2 * time.Second}}}
	if err := plan.Validate(); err != nil {
		return pluginapi.ExecutionPlan{}, pluginapi.WrapError(pluginapi.ErrorPlanInvalid, "fake plugin generated an invalid plan", err)
	}
	return plan, nil
}

func (p *Plugin) ParseResult(_ context.Context, plan pluginapi.ExecutionPlan, transcript pluginapi.Transcript) (pluginapi.OperationResult, error) {
	if plan.Vendor != p.vendor || plan.PluginName != p.Metadata().Name {
		return pluginapi.OperationResult{}, pluginapi.NewError(pluginapi.ErrorInvalidRequest, "plan does not belong to fake plugin")
	}
	if err := transcript.ValidateAgainst(plan); err != nil {
		return pluginapi.OperationResult{}, pluginapi.WrapError(pluginapi.ErrorOutputUnparsable, "execution transcript is invalid", err)
	}
	commandResults := make([]pluginapi.CommandExecution, 0, len(transcript.Commands))
	outputs := make([]string, 0, len(transcript.Commands))
	result := pluginapi.OperationResult{Status: pluginapi.ResultSuccess, StartedAt: transcript.StartedAt, FinishedAt: transcript.FinishedAt}
	for _, record := range transcript.Commands {
		commandResults = append(commandResults, pluginapi.CommandExecution{Sequence: record.Sequence, Succeeded: record.Succeeded, OutputTruncated: record.OutputTruncated, ErrorCode: record.ErrorCode, Duration: record.Duration})
		outputs = append(outputs, record.Output)
		if !record.Succeeded && result.Status == pluginapi.ResultSuccess {
			result.Status, result.ErrorCode, result.ErrorMessage = pluginapi.ResultFailed, record.ErrorCode, "fake command execution failed"
		}
	}
	result.Commands, result.Data = commandResults, map[string]any{"outputs": outputs}
	if err := result.Validate(); err != nil {
		return pluginapi.OperationResult{}, pluginapi.WrapError(pluginapi.ErrorOutputUnparsable, "fake normalized result is invalid", err)
	}
	return result, nil
}

var _ pluginapi.Plugin = (*Plugin)(nil)
