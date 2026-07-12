package operationsvc

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/dylanLi233/switch-manager/internal/domain/device"
	"github.com/dylanLi233/switch-manager/internal/domain/operation"
	"github.com/dylanLi233/switch-manager/internal/pluginregistry"
	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
)

type planInput struct {
	Name        operation.Name
	Class       operation.Class
	DeviceID    string
	Parameters  map[string]any
	SaveConfig  bool
	ConfirmRisk bool
	MainPlanID  string
	SavePlanID  string
}

type preparedOperation struct {
	Device     device.Device
	DeviceInfo pluginapi.DeviceInfo
	Plugin     pluginapi.Plugin
	Metadata   pluginapi.Metadata
	MainPlan   pluginapi.ExecutionPlan
	SavePlan   *pluginapi.ExecutionPlan
	MainHash   string
	SaveHash   string
	AuditPlan  []byte
}

// DeviceReader is the minimum inventory contract needed by planning.
type DeviceReader interface {
	Get(context.Context, string) (device.Device, error)
}

// Planner performs device, plugin, capability, plan, and risk preflight.
type Planner struct {
	devices  DeviceReader
	registry PluginRegistry
}

func NewPlanner(devices DeviceReader, registry PluginRegistry) (*Planner, error) {
	if devices == nil {
		return nil, errors.New("device repository is required")
	}
	if registry == nil {
		return nil, errors.New("plugin registry is required")
	}
	return &Planner{devices: devices, registry: registry}, nil
}

func (p *Planner) prepare(ctx context.Context, input planInput) (preparedOperation, error) {
	if ctx == nil {
		return preparedOperation{}, errors.New("context is required")
	}
	if strings.TrimSpace(string(input.Name)) == "" || strings.TrimSpace(input.DeviceID) == "" || strings.TrimSpace(input.MainPlanID) == "" {
		return preparedOperation{}, apperror.New(apperror.CodeValidationError, "operation, device, and plan IDs are required")
	}
	if input.SaveConfig && strings.TrimSpace(input.SavePlanID) == "" {
		return preparedOperation{}, apperror.New(apperror.CodeValidationError, "save plan ID is required")
	}

	managed, err := p.devices.Get(ctx, input.DeviceID)
	if err != nil {
		return preparedOperation{}, err
	}
	if err := validateDeviceForOperation(managed, input.Class); err != nil {
		return preparedOperation{}, err
	}
	vendor, err := pluginregistry.VendorFromDomain(managed.Vendor)
	if err != nil {
		return preparedOperation{}, apperror.Wrap(apperror.CodePluginNotFound, "", err)
	}
	info := pluginapi.DeviceInfo{Vendor: vendor, Model: managed.Model, OSVersion: managed.OSVersion}
	plugin, err := p.registry.Resolve(vendor)
	if err != nil {
		return preparedOperation{}, mapPlanningError(err)
	}
	metadata := plugin.Metadata().Clone()

	main, err := p.buildPlan(ctx, plugin, metadata, info, input.MainPlanID, input.DeviceID, pluginapi.OperationName(input.Name), toPluginClass(input.Class), input.Parameters, input.SaveConfig)
	if err != nil {
		return preparedOperation{}, err
	}
	if err := enforceRisk(main.RiskLevel, input.ConfirmRisk); err != nil {
		return preparedOperation{}, err
	}

	var save *pluginapi.ExecutionPlan
	if input.SaveConfig {
		plannedSave, saveErr := p.buildPlan(ctx, plugin, metadata, info, input.SavePlanID, input.DeviceID, SaveConfigOperation, pluginapi.ClassConfig, nil, false)
		if saveErr != nil {
			return preparedOperation{}, saveErr
		}
		if err := enforceRisk(plannedSave.RiskLevel, input.ConfirmRisk); err != nil {
			return preparedOperation{}, err
		}
		save = &plannedSave
	}

	mainHash, err := hashPlan(main)
	if err != nil {
		return preparedOperation{}, apperror.Wrap(apperror.CodeInternalError, "", err)
	}
	saveHash := ""
	if save != nil {
		saveHash, err = hashPlan(*save)
		if err != nil {
			return preparedOperation{}, apperror.Wrap(apperror.CodeInternalError, "", err)
		}
	}
	auditPlan, err := marshalPlanBundle(main, save)
	if err != nil {
		return preparedOperation{}, apperror.Wrap(apperror.CodeInternalError, "", err)
	}
	return preparedOperation{
		Device: managed, DeviceInfo: info, Plugin: plugin, Metadata: metadata,
		MainPlan: main, SavePlan: save, MainHash: mainHash, SaveHash: saveHash,
		AuditPlan: auditPlan,
	}, nil
}

func (p *Planner) buildPlan(ctx context.Context, plugin pluginapi.Plugin, metadata pluginapi.Metadata, info pluginapi.DeviceInfo, planID, deviceID string, name pluginapi.OperationName, class pluginapi.OperationClass, parameters map[string]any, saveConfig bool) (pluginapi.ExecutionPlan, error) {
	if !metadata.Declares(name) {
		if name == SaveConfigOperation {
			return pluginapi.ExecutionPlan{}, apperror.New(apperror.CodeCapabilityNotSupported, "")
		}
		return pluginapi.ExecutionPlan{}, apperror.New(apperror.CodeUnsupportedOperation, "")
	}
	capability, err := p.registry.LookupCapability(ctx, info.Vendor, info, name)
	if err != nil {
		return pluginapi.ExecutionPlan{}, mapPlanningError(err)
	}
	if capability.Level == pluginapi.SupportUnsupported {
		return pluginapi.ExecutionPlan{}, apperror.New(apperror.CodeCapabilityNotSupported, "")
	}
	plan, err := plugin.BuildPlan(ctx, pluginapi.PlanRequest{PlanID: planID, DeviceID: deviceID, Device: info.Clone(), Operation: name, Class: class, Parameters: cloneMap(parameters), SaveConfig: saveConfig})
	if err != nil {
		return pluginapi.ExecutionPlan{}, mapPlanningError(err)
	}
	if err := validatePluginPlan(plan, metadata, info.Vendor, planID, deviceID, name, class, saveConfig); err != nil {
		return pluginapi.ExecutionPlan{}, apperror.Wrap(apperror.CodeInternalError, "", err)
	}
	return plan, nil
}

func validatePluginPlan(plan pluginapi.ExecutionPlan, metadata pluginapi.Metadata, vendor pluginapi.Vendor, planID, deviceID string, name pluginapi.OperationName, class pluginapi.OperationClass, saveConfig bool) error {
	if err := plan.Validate(); err != nil {
		return fmt.Errorf("validate plugin plan: %w", err)
	}
	if plan.PlanID != planID || plan.DeviceID != deviceID || plan.Vendor != vendor || plan.PluginName != metadata.Name || plan.PluginVersion != metadata.PluginVersion.String() || plan.Operation != name || plan.Class != class || plan.SaveConfig != saveConfig {
		return errors.New("plugin returned a plan that does not match the preflight request")
	}
	return nil
}

func validateDeviceForOperation(value device.Device, class operation.Class) error {
	if err := value.Validate(); err != nil {
		return apperror.Wrap(apperror.CodeInternalError, "", err)
	}
	switch value.Status {
	case device.StatusDisabled:
		return apperror.New(apperror.CodeDeviceDisabled, "")
	case device.StatusUnreachable:
		return apperror.New(apperror.CodeDeviceUnreachable, "")
	}
	if class != operation.ClassQuery && value.IdentityStatus != device.IdentityVerified {
		return apperror.New(apperror.CodeIdentityMismatch, "")
	}
	return nil
}

func enforceRisk(level pluginapi.RiskLevel, confirmed bool) error {
	switch level {
	case pluginapi.RiskBlocked:
		return apperror.New(apperror.CodeDangerousCommandBlocked, "")
	case pluginapi.RiskHigh:
		if !confirmed {
			return apperror.New(apperror.CodeRiskConfirmationRequired, "")
		}
	}
	return nil
}

func toPluginClass(class operation.Class) pluginapi.OperationClass { return pluginapi.OperationClass(class) }

func mapPlanningError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pluginregistry.ErrPluginNotFound) {
		return apperror.Wrap(apperror.CodePluginNotFound, "", err)
	}
	switch {
	case pluginapi.IsErrorCode(err, pluginapi.ErrorInvalidRequest):
		return apperror.Wrap(apperror.CodeValidationError, "", err)
	case pluginapi.IsErrorCode(err, pluginapi.ErrorUnsupportedOperation):
		return apperror.Wrap(apperror.CodeOperationNotImplemented, "", err)
	case pluginapi.IsErrorCode(err, pluginapi.ErrorPlanInvalid):
		return apperror.Wrap(apperror.CodeInternalError, "", err)
	default:
		return apperror.Wrap(apperror.CodeInternalError, "", err)
	}
}

func cloneMap(source map[string]any) map[string]any {
	if source == nil {
		return nil
	}
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
