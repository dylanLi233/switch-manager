// Package fake supplies a deterministic contract plugin for tests and early
// integration. It contains no real Huawei or H3C commands.
package fake

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/dylanLi233/switch-manager/internal/domain/vlan"
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
		PluginVersion: pluginapi.Version{Major: 1, Minor: 3, Patch: 0},
		SDKVersion:    pluginapi.CurrentSDKVersion(),
		Operations: []pluginapi.OperationName{
			OperationEchoQuery, OperationEchoConfig, OperationSaveConfig,
			pluginapi.OperationVLANList, pluginapi.OperationVLANGet,
			pluginapi.OperationVLANCreate, pluginapi.OperationVLANUpdate,
			pluginapi.OperationVLANDelete,
			pluginapi.OperationInterfaceList, pluginapi.OperationInterfaceGet,
			pluginapi.OperationInterfaceEnable, pluginapi.OperationInterfaceDisable,
			pluginapi.OperationInterfaceAccess, pluginapi.OperationInterfaceTrunk,
			pluginapi.OperationInterfaceVLANAdd, pluginapi.OperationInterfaceVLANRemove,
			pluginapi.OperationRouteList, pluginapi.OperationRouteGet,
			pluginapi.OperationRouteCreate, pluginapi.OperationRouteUpdate,
			pluginapi.OperationRouteDelete,
			pluginapi.OperationACLList, pluginapi.OperationACLGet,
			pluginapi.OperationACLCreate, pluginapi.OperationACLUpdate,
			pluginapi.OperationACLDelete,
		},
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
	capabilities := []pluginapi.Capability{{Operation: OperationEchoQuery, Level: pluginapi.SupportSupported}}
	operations := []pluginapi.OperationName{
		OperationEchoConfig, OperationSaveConfig,
		pluginapi.OperationVLANList, pluginapi.OperationVLANGet,
		pluginapi.OperationVLANCreate, pluginapi.OperationVLANUpdate, pluginapi.OperationVLANDelete,
		pluginapi.OperationInterfaceList, pluginapi.OperationInterfaceGet,
		pluginapi.OperationInterfaceEnable, pluginapi.OperationInterfaceDisable,
		pluginapi.OperationInterfaceAccess, pluginapi.OperationInterfaceTrunk,
		pluginapi.OperationInterfaceVLANAdd, pluginapi.OperationInterfaceVLANRemove,
		pluginapi.OperationRouteList, pluginapi.OperationRouteGet,
		pluginapi.OperationRouteCreate, pluginapi.OperationRouteUpdate, pluginapi.OperationRouteDelete,
		pluginapi.OperationACLList, pluginapi.OperationACLGet,
		pluginapi.OperationACLCreate, pluginapi.OperationACLUpdate, pluginapi.OperationACLDelete,
	}
	for _, operation := range operations {
		level, reason := pluginapi.SupportUnsupported, "fake model is unknown"
		if info.Model == "FAKE-SW" {
			level, reason = pluginapi.SupportSupported, ""
		}
		capabilities = append(capabilities, pluginapi.Capability{Operation: operation, Level: level, Reason: reason})
	}
	return pluginapi.NewCapabilitySet(capabilities...)
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
	case OperationEchoQuery, OperationEchoConfig:
		message, err := requireMessage(request.Parameters)
		if err != nil {
			return pluginapi.ExecutionPlan{}, err
		}
		if request.Operation == OperationEchoQuery {
			if request.Class != pluginapi.ClassQuery {
				return pluginapi.ExecutionPlan{}, pluginapi.NewError(pluginapi.ErrorInvalidRequest, "diagnostic.echo requires QUERY class")
			}
			commandText = "fake.echo.query " + strconv.Quote(message)
		} else {
			if request.Class != pluginapi.ClassConfig {
				return pluginapi.ExecutionPlan{}, pluginapi.NewError(pluginapi.ErrorInvalidRequest, "configuration.echo requires CONFIG class")
			}
			commandText, risk, enterConfig = "fake.echo.config "+strconv.Quote(message), pluginapi.RiskMedium, true
		}
	case OperationSaveConfig:
		if request.Class != pluginapi.ClassConfig || request.SaveConfig || len(request.Parameters) != 0 {
			return pluginapi.ExecutionPlan{}, pluginapi.NewError(pluginapi.ErrorInvalidRequest, "config.save requires CONFIG class, no parameters, and cannot recursively request save_config")
		}
		commandText, risk = "fake.config.save", pluginapi.RiskMedium
	case pluginapi.OperationVLANList:
		if request.Class != pluginapi.ClassQuery || len(request.Parameters) != 0 {
			return pluginapi.ExecutionPlan{}, pluginapi.NewError(pluginapi.ErrorInvalidRequest, "vlan.list requires QUERY class and no parameters")
		}
		commandText = "fake.vlan.list"
	case pluginapi.OperationVLANGet:
		if request.Class != pluginapi.ClassQuery {
			return pluginapi.ExecutionPlan{}, pluginapi.NewError(pluginapi.ErrorInvalidRequest, "vlan.get requires QUERY class")
		}
		commandText, err = vlanCommand("fake.vlan.get", request.Parameters, false, false)
	case pluginapi.OperationVLANCreate:
		if request.Class != pluginapi.ClassConfig {
			return pluginapi.ExecutionPlan{}, pluginapi.NewError(pluginapi.ErrorInvalidRequest, "vlan.create requires CONFIG class")
		}
		commandText, err = vlanCommand("fake.vlan.create", request.Parameters, true, false)
		risk, enterConfig = pluginapi.RiskMedium, true
	case pluginapi.OperationVLANUpdate:
		if request.Class != pluginapi.ClassConfig {
			return pluginapi.ExecutionPlan{}, pluginapi.NewError(pluginapi.ErrorInvalidRequest, "vlan.update requires CONFIG class")
		}
		commandText, err = vlanCommand("fake.vlan.update", request.Parameters, true, true)
		risk, enterConfig = pluginapi.RiskMedium, true
	case pluginapi.OperationVLANDelete:
		if request.Class != pluginapi.ClassConfig {
			return pluginapi.ExecutionPlan{}, pluginapi.NewError(pluginapi.ErrorInvalidRequest, "vlan.delete requires CONFIG class")
		}
		commandText, err = vlanCommand("fake.vlan.delete", request.Parameters, false, false)
		risk, enterConfig = pluginapi.RiskMedium, true
	case pluginapi.OperationInterfaceList, pluginapi.OperationInterfaceGet,
		pluginapi.OperationInterfaceEnable, pluginapi.OperationInterfaceDisable,
		pluginapi.OperationInterfaceAccess, pluginapi.OperationInterfaceTrunk,
		pluginapi.OperationInterfaceVLANAdd, pluginapi.OperationInterfaceVLANRemove:
		commandText, risk, enterConfig, err = interfaceCommand(p, request)
	case pluginapi.OperationRouteList, pluginapi.OperationRouteGet,
		pluginapi.OperationRouteCreate, pluginapi.OperationRouteUpdate, pluginapi.OperationRouteDelete,
		pluginapi.OperationACLList, pluginapi.OperationACLGet,
		pluginapi.OperationACLCreate, pluginapi.OperationACLUpdate, pluginapi.OperationACLDelete:
		commandText, risk, enterConfig, err = routeACLCommand(p, request)
	default:
		return pluginapi.ExecutionPlan{}, pluginapi.NewError(pluginapi.ErrorUnsupportedOperation, "operation is not declared")
	}
	if err != nil {
		return pluginapi.ExecutionPlan{}, err
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
	result := pluginapi.OperationResult{Status: pluginapi.ResultSuccess, StartedAt: transcript.StartedAt, FinishedAt: transcript.FinishedAt}
	for _, record := range transcript.Commands {
		commandResults = append(commandResults, pluginapi.CommandExecution{Sequence: record.Sequence, Succeeded: record.Succeeded, OutputTruncated: record.OutputTruncated, ErrorCode: record.ErrorCode, Duration: record.Duration})
		if !record.Succeeded && result.Status == pluginapi.ResultSuccess {
			result.Status, result.ErrorCode, result.ErrorMessage = pluginapi.ResultFailed, record.ErrorCode, "fake command execution failed"
		}
	}
	result.Commands = commandResults
	if result.Status == pluginapi.ResultSuccess {
		last := transcript.Commands[len(transcript.Commands)-1]
		if isVLANOperation(plan.Operation) || isInterfaceOperation(plan.Operation) || isRouteACLOperation(plan.Operation) {
			var data any
			if err := json.Unmarshal([]byte(last.Output), &data); err != nil {
				return pluginapi.OperationResult{}, pluginapi.WrapError(pluginapi.ErrorOutputUnparsable, "fake structured output is invalid JSON", err)
			}
			switch {
			case isVLANOperation(plan.Operation):
				if err := validateVLANOutput(plan.Operation, data); err != nil {
					return pluginapi.OperationResult{}, pluginapi.WrapError(pluginapi.ErrorOutputUnparsable, "fake VLAN output has an invalid schema", err)
				}
			case isInterfaceOperation(plan.Operation):
				if err := validateInterfaceOutput(p, plan.Operation, data); err != nil {
					return pluginapi.OperationResult{}, pluginapi.WrapError(pluginapi.ErrorOutputUnparsable, "fake interface output has an invalid schema", err)
				}
			case isRouteACLOperation(plan.Operation):
				if err := validateRouteACLOutput(p, plan.Operation, data); err != nil {
					return pluginapi.OperationResult{}, pluginapi.WrapError(pluginapi.ErrorOutputUnparsable, "fake route or ACL output has an invalid schema", err)
				}
			}
			result.Data = data
		} else {
			outputs := make([]string, 0, len(transcript.Commands))
			for _, record := range transcript.Commands {
				outputs = append(outputs, record.Output)
			}
			result.Data = map[string]any{"outputs": outputs}
		}
	}
	if err := result.Validate(); err != nil {
		return pluginapi.OperationResult{}, pluginapi.WrapError(pluginapi.ErrorOutputUnparsable, "fake normalized result is invalid", err)
	}
	return result, nil
}

func requireMessage(parameters map[string]any) (string, error) {
	if len(parameters) != 1 {
		return "", pluginapi.NewError(pluginapi.ErrorInvalidRequest, "message is the only supported parameter")
	}
	message, ok := parameters["message"].(string)
	if !ok || strings.TrimSpace(message) == "" || len(message) > 256 || strings.ContainsAny(message, "\r\n") {
		return "", pluginapi.NewError(pluginapi.ErrorInvalidRequest, "message must be a non-empty single-line string up to 256 bytes")
	}
	return message, nil
}

type vlanPayload struct {
	VLANID int    `json:"vlan_id"`
	Name   string `json:"name,omitempty"`
}

func vlanCommand(prefix string, parameters map[string]any, allowName, requireName bool) (string, error) {
	allowed := 1
	if allowName {
		allowed = 2
	}
	if len(parameters) < 1 || len(parameters) > allowed {
		return "", pluginapi.NewError(pluginapi.ErrorInvalidRequest, "VLAN parameters are invalid")
	}
	id, err := integerParameter(parameters["vlan_id"])
	if err != nil || vlan.ValidateID(id) != nil {
		return "", pluginapi.NewError(pluginapi.ErrorInvalidRequest, "vlan_id must be between 1 and 4094")
	}
	name := ""
	if raw, exists := parameters["name"]; exists {
		if !allowName {
			return "", pluginapi.NewError(pluginapi.ErrorInvalidRequest, "name is not supported for this operation")
		}
		var ok bool
		name, ok = raw.(string)
		if !ok {
			return "", pluginapi.NewError(pluginapi.ErrorInvalidRequest, "VLAN name must be a string")
		}
	}
	name, err = vlan.NormalizeName(name, requireName)
	if err != nil {
		return "", pluginapi.WrapError(pluginapi.ErrorInvalidRequest, "VLAN name is invalid", err)
	}
	for key := range parameters {
		if key != "vlan_id" && key != "name" {
			return "", pluginapi.NewError(pluginapi.ErrorInvalidRequest, "unknown VLAN parameter")
		}
	}
	encoded, err := json.Marshal(vlanPayload{VLANID: id, Name: name})
	if err != nil {
		return "", pluginapi.WrapError(pluginapi.ErrorPlanInvalid, "encode fake VLAN command", err)
	}
	return prefix + " " + string(encoded), nil
}

func integerParameter(value any) (int, error) {
	switch typed := value.(type) {
	case int:
		return typed, nil
	case int32:
		return int(typed), nil
	case int64:
		return int(typed), nil
	case float64:
		if math.Trunc(typed) != typed || typed < -1<<31 || typed > 1<<31-1 {
			return 0, fmt.Errorf("not an integer")
		}
		return int(typed), nil
	case json.Number:
		parsed, err := typed.Int64()
		return int(parsed), err
	default:
		return 0, fmt.Errorf("unsupported integer type %T", value)
	}
}

func isVLANOperation(name pluginapi.OperationName) bool {
	switch name {
	case pluginapi.OperationVLANList, pluginapi.OperationVLANGet, pluginapi.OperationVLANCreate, pluginapi.OperationVLANUpdate, pluginapi.OperationVLANDelete:
		return true
	default:
		return false
	}
}

func validateVLANOutput(operation pluginapi.OperationName, data any) error {
	object, ok := data.(map[string]any)
	if !ok {
		return fmt.Errorf("result must be an object")
	}
	switch operation {
	case pluginapi.OperationVLANList:
		items, ok := object["vlans"].([]any)
		if !ok {
			return fmt.Errorf("vlans array is required")
		}
		for _, item := range items {
			if err := validateVLANObject(item); err != nil {
				return err
			}
		}
	case pluginapi.OperationVLANGet, pluginapi.OperationVLANCreate, pluginapi.OperationVLANUpdate:
		if err := validateVLANObject(object["vlan"]); err != nil {
			return err
		}
	case pluginapi.OperationVLANDelete:
		deleted, ok := object["deleted"].(bool)
		if !ok || !deleted {
			return fmt.Errorf("deleted=true is required")
		}
		id, err := integerParameter(object["vlan_id"])
		if err != nil || vlan.ValidateID(id) != nil {
			return fmt.Errorf("valid vlan_id is required")
		}
	}
	return nil
}

func validateVLANObject(value any) error {
	object, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("VLAN object is required")
	}
	id, err := integerParameter(object["vlan_id"])
	if err != nil || vlan.ValidateID(id) != nil {
		return fmt.Errorf("valid vlan_id is required")
	}
	name, ok := object["name"].(string)
	if !ok {
		return fmt.Errorf("VLAN name is required")
	}
	_, err = vlan.NormalizeName(name, false)
	return err
}

var _ pluginapi.Plugin = (*Plugin)(nil)
