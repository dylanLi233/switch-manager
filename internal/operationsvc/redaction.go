package operationsvc

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dylanLi233/switch-manager/internal/domain/operation"
	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
)

type commandView struct {
	Sequence      int    `json:"sequence"`
	Command       string `json:"command"`
	CommandSHA256 string `json:"command_sha256"`
	Sensitive     bool   `json:"sensitive"`
	ExpectedMode  string `json:"expected_mode,omitempty"`
	TimeoutMS     int64  `json:"timeout_ms"`
}

type planView struct {
	PlanID          string                   `json:"plan_id"`
	DeviceID        string                   `json:"device_id"`
	Vendor          pluginapi.Vendor         `json:"vendor"`
	PluginName      string                   `json:"plugin_name"`
	PluginVersion   string                   `json:"plugin_version"`
	Operation       pluginapi.OperationName  `json:"operation"`
	Class           pluginapi.OperationClass `json:"class"`
	EnterConfigMode bool                     `json:"enter_config_mode"`
	SaveConfig      bool                     `json:"save_config"`
	RiskLevel       pluginapi.RiskLevel      `json:"risk_level"`
	Warnings        []string                 `json:"warnings,omitempty"`
	Commands        []commandView            `json:"commands"`
}

type planBundleView struct {
	Main planView  `json:"main"`
	Save *planView `json:"save,omitempty"`
}

func redactPlan(plan pluginapi.ExecutionPlan) planView {
	commands := make([]commandView, len(plan.Commands))
	for index, command := range plan.Commands {
		sum := sha256.Sum256([]byte(command.Text))
		text := command.Text
		if command.Sensitive {
			text = "<redacted>"
		}
		commands[index] = commandView{
			Sequence: command.Sequence, Command: text,
			CommandSHA256: hex.EncodeToString(sum[:]), Sensitive: command.Sensitive,
			ExpectedMode: command.ExpectedMode, TimeoutMS: command.Timeout.Milliseconds(),
		}
	}
	return planView{
		PlanID: plan.PlanID, DeviceID: plan.DeviceID, Vendor: plan.Vendor,
		PluginName: plan.PluginName, PluginVersion: plan.PluginVersion,
		Operation: plan.Operation, Class: plan.Class, EnterConfigMode: plan.EnterConfigMode,
		SaveConfig: plan.SaveConfig, RiskLevel: plan.RiskLevel,
		Warnings: append([]string(nil), plan.Warnings...), Commands: commands,
	}
}

func marshalPlanBundle(main pluginapi.ExecutionPlan, save *pluginapi.ExecutionPlan) (json.RawMessage, error) {
	bundle := planBundleView{Main: redactPlan(main)}
	if save != nil {
		view := redactPlan(*save)
		bundle.Save = &view
	}
	data, err := json.Marshal(bundle)
	return json.RawMessage(data), err
}

func marshalAuditRequest(request operation.Request, requestFingerprint string) (json.RawMessage, error) {
	payload := map[string]any{
		"operation": request.Name, "class": request.Class, "device_id": request.DeviceID,
		"parameters": redactValue(request.Parameters), "execution_mode": request.ExecutionMode,
		"dry_run": request.DryRun, "save_config": request.SaveConfig,
		"confirm_risk": request.ConfirmRisk, "request_fingerprint": requestFingerprint,
	}
	if request.IdempotencyKey != "" {
		sum := sha256.Sum256([]byte(request.IdempotencyKey))
		payload["idempotency_key_sha256"] = hex.EncodeToString(sum[:])
	}
	data, err := json.Marshal(payload)
	return json.RawMessage(data), err
}

func redactValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			if sensitiveKey(key) {
				result[key] = "<redacted>"
			} else {
				result[key] = redactValue(item)
			}
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = redactValue(item)
		}
		return result
	default:
		return typed
	}
}

func sensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	for _, token := range []string{"password", "secret", "private_key", "token", "community", "passphrase", "credential", "command"} {
		if strings.Contains(normalized, token) {
			return true
		}
	}
	return false
}

func hashPlan(plan pluginapi.ExecutionPlan) (string, error) {
	data, err := json.Marshal(plan)
	if err != nil {
		return "", fmt.Errorf("marshal execution plan: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
