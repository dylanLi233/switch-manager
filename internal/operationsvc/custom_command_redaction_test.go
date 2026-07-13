package operationsvc

import (
	"strings"
	"testing"
	"time"

	"github.com/dylanLi233/switch-manager/internal/domain/operation"
	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
)

func TestCustomCommandAuditRequestRedactsCommandArray(t *testing.T) {
	payload, err := marshalAuditRequest(operation.Request{
		Name: operation.Name(pluginapi.OperationCommandExecuteConfig),
		Class: operation.ClassConfig,
		DeviceID: "device-id",
		Parameters: map[string]any{"commands": []string{"fake.set password secret-value"}},
		ExecutionMode: operation.ExecutionModeSync,
	}, "fingerprint")
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	if strings.Contains(text, "fake.set password") || strings.Contains(text, "secret-value") || !strings.Contains(text, `"commands":"<redacted>"`) {
		t.Fatalf("payload=%s", text)
	}
}

func TestSensitiveCustomCommandPlanKeepsOnlyHash(t *testing.T) {
	view := redactPlan(pluginapi.ExecutionPlan{
		PlanID: "plan-id", DeviceID: "device-id", Vendor: pluginapi.VendorHuawei,
		PluginName: "fake-huawei", PluginVersion: "1.5.0",
		Operation: pluginapi.OperationCommandExecuteConfig, Class: pluginapi.ClassConfig,
		EnterConfigMode: true, RiskLevel: pluginapi.RiskMedium,
		Commands: []pluginapi.PlannedCommand{{Sequence: 1, Text: "fake.set password secret-value", Timeout: time.Second, Sensitive: true}},
	})
	if len(view.Commands) != 1 || view.Commands[0].Command != "<redacted>" || view.Commands[0].CommandSHA256 == "" || !view.Commands[0].Sensitive {
		t.Fatalf("view=%+v", view)
	}
}
