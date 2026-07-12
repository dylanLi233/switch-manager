package operationsvc

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/dylanLi233/switch-manager/internal/domain/operation"
	"github.com/dylanLi233/switch-manager/internal/domain/task"
)

const payloadVersion = 1

type requestSnapshot struct {
	Name          operation.Name          `json:"name"`
	Class         operation.Class         `json:"class"`
	DeviceID      string                  `json:"device_id"`
	Parameters    map[string]any          `json:"parameters,omitempty"`
	ExecutionMode operation.ExecutionMode `json:"execution_mode"`
	DryRun        bool                    `json:"dry_run"`
	SaveConfig    bool                    `json:"save_config"`
	ConfirmRisk   bool                    `json:"confirm_risk"`
}

type taskPayload struct {
	Version            int             `json:"version"`
	Request            requestSnapshot `json:"request"`
	RequestFingerprint string          `json:"request_fingerprint"`
	RequestID          string          `json:"request_id"`
	MainPlanID         string          `json:"main_plan_id"`
	MainPlanSHA256     string          `json:"main_plan_sha256"`
	SavePlanID         string          `json:"save_plan_id,omitempty"`
	SavePlanSHA256     string          `json:"save_plan_sha256,omitempty"`
}

func snapshotRequest(value operation.Request) requestSnapshot {
	parameters := make(map[string]any, len(value.Parameters))
	for key, item := range value.Parameters {
		parameters[key] = item
	}
	return requestSnapshot{
		Name: value.Name, Class: value.Class, DeviceID: value.DeviceID,
		Parameters: parameters, ExecutionMode: value.ExecutionMode,
		DryRun: value.DryRun, SaveConfig: value.SaveConfig, ConfirmRisk: value.ConfirmRisk,
	}
}

func fingerprint(snapshot requestSnapshot) (string, error) {
	data, err := json.Marshal(snapshot)
	if err != nil {
		return "", fmt.Errorf("marshal request fingerprint: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func encodeTaskPayload(payload taskPayload) (json.RawMessage, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(data), nil
}

func decodeTaskPayload(value task.Persisted) (taskPayload, error) {
	var payload taskPayload
	if len(value.Payload) == 0 {
		return payload, errors.New("operation task payload is empty")
	}
	if err := json.Unmarshal(value.Payload, &payload); err != nil {
		return payload, fmt.Errorf("decode operation task payload: %w", err)
	}
	if payload.Version != payloadVersion {
		return payload, fmt.Errorf("unsupported operation task payload version %d", payload.Version)
	}
	if payload.RequestFingerprint == "" || payload.RequestID == "" || payload.MainPlanID == "" || payload.MainPlanSHA256 == "" {
		return payload, errors.New("operation task payload is incomplete")
	}
	if value.SaveConfig && (payload.SavePlanID == "" || payload.SavePlanSHA256 == "") {
		return payload, errors.New("save-config task payload is incomplete")
	}
	if !value.SaveConfig && (payload.SavePlanID != "" || payload.SavePlanSHA256 != "") {
		return payload, errors.New("non-save task unexpectedly contains a save plan")
	}
	if payload.Request.Name != value.Operation || payload.Request.DeviceID != value.TargetID ||
		payload.Request.ExecutionMode != value.ExecutionMode || payload.Request.DryRun != value.DryRun ||
		payload.Request.SaveConfig != value.SaveConfig {
		return payload, errors.New("operation task payload does not match persisted task fields")
	}
	return payload, nil
}
