package pluginregistry

import (
	"fmt"

	"github.com/dylanLi233/switch-manager/internal/domain/device"
	"github.com/dylanLi233/switch-manager/internal/domain/operation"
	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
)

// VendorFromDomain converts a core vendor to the public SDK type.
func VendorFromDomain(vendor device.Vendor) (pluginapi.Vendor, error) {
	if err := vendor.Validate(); err != nil {
		return "", err
	}
	result := pluginapi.Vendor(vendor)
	if err := result.Validate(); err != nil {
		return "", err
	}
	return result, nil
}

// VendorToDomain converts a validated SDK vendor to the core type.
func VendorToDomain(vendor pluginapi.Vendor) (device.Vendor, error) {
	if err := vendor.Validate(); err != nil {
		return "", err
	}
	result := device.Vendor(vendor)
	if err := result.Validate(); err != nil {
		return "", err
	}
	return result, nil
}

// PlanToDomain validates and copies an SDK plan into the internal domain.
func PlanToDomain(plan pluginapi.ExecutionPlan) (operation.ExecutionPlan, error) {
	if err := plan.Validate(); err != nil {
		return operation.ExecutionPlan{}, fmt.Errorf("validate SDK plan: %w", err)
	}
	vendor, err := VendorToDomain(plan.Vendor)
	if err != nil {
		return operation.ExecutionPlan{}, err
	}
	commands := make([]operation.PlannedCommand, len(plan.Commands))
	for i, command := range plan.Commands {
		commands[i] = operation.PlannedCommand{
			Sequence: command.Sequence, Text: command.Text, Sensitive: command.Sensitive,
			ExpectedMode: command.ExpectedMode, Timeout: command.Timeout,
		}
	}
	result := operation.ExecutionPlan{
		PlanID: plan.PlanID, DeviceID: plan.DeviceID, Vendor: vendor,
		PluginName: plan.PluginName, PluginVersion: plan.PluginVersion,
		Operation: operation.Name(plan.Operation), Class: operation.Class(plan.Class),
		Commands: commands, EnterConfigMode: plan.EnterConfigMode,
		SaveConfig: plan.SaveConfig, RiskLevel: operation.RiskLevel(plan.RiskLevel),
		Warnings: append([]string(nil), plan.Warnings...),
	}
	if err := result.Validate(); err != nil {
		return operation.ExecutionPlan{}, fmt.Errorf("validate domain plan: %w", err)
	}
	return result, nil
}

// ResultToDomain validates and copies a plugin result into the internal domain.
func ResultToDomain(result pluginapi.OperationResult) (operation.Result, error) {
	if err := result.Validate(); err != nil {
		return operation.Result{}, fmt.Errorf("validate SDK result: %w", err)
	}
	commands := make([]operation.CommandExecution, len(result.Commands))
	for i, command := range result.Commands {
		commands[i] = operation.CommandExecution{
			Sequence: command.Sequence, Succeeded: command.Succeeded,
			OutputTruncated: command.OutputTruncated, ErrorCode: command.ErrorCode,
			Duration: command.Duration,
		}
	}
	converted := operation.Result{
		Status: operation.ResultStatus(result.Status), Data: result.Data, Commands: commands,
		ErrorCode: result.ErrorCode, ErrorMessage: result.ErrorMessage,
		StartedAt: result.StartedAt, FinishedAt: result.FinishedAt,
	}
	if err := converted.Validate(); err != nil {
		return operation.Result{}, fmt.Errorf("validate domain result: %w", err)
	}
	return converted, nil
}
