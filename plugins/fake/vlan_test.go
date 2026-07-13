package fake

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
)

func TestVLANPlanUsesStructuredPayload(t *testing.T) {
	plugin, _ := New(pluginapi.VendorHuawei)
	request := pluginapi.PlanRequest{
		PlanID: "plan", DeviceID: "device",
		Device: pluginapi.DeviceInfo{Vendor: pluginapi.VendorHuawei, Model: "FAKE-SW"},
		Operation: pluginapi.OperationVLANCreate, Class: pluginapi.ClassConfig,
		Parameters: map[string]any{"vlan_id": 100, "name": "office"},
	}
	plan, err := plugin.BuildPlan(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if plan.RiskLevel != pluginapi.RiskMedium || !plan.EnterConfigMode || !strings.HasPrefix(plan.Commands[0].Text, `fake.vlan.create {`) {
		t.Fatalf("plan=%+v", plan)
	}
	request.Parameters["vlan_id"] = float64(100)
	rebuilt, err := plugin.BuildPlan(context.Background(), request)
	if err != nil || rebuilt.Commands[0].Text != plan.Commands[0].Text {
		t.Fatalf("rebuilt=%+v err=%v", rebuilt, err)
	}
}

func TestVLANPlanRejectsInjectionAndInvalidID(t *testing.T) {
	plugin, _ := New(pluginapi.VendorHuawei)
	for _, parameters := range []map[string]any{
		{"vlan_id": 0, "name": "office"},
		{"vlan_id": 4095, "name": "office"},
		{"vlan_id": 100, "name": "office\nundo everything"},
		{"vlan_id": 100, "name": `office"; fake.vlan.delete`},
		{"vlan_id": 100, "name": "office", "command": "raw"},
	} {
		_, err := plugin.BuildPlan(context.Background(), pluginapi.PlanRequest{
			PlanID: "plan", DeviceID: "device",
			Device: pluginapi.DeviceInfo{Vendor: pluginapi.VendorHuawei, Model: "FAKE-SW"},
			Operation: pluginapi.OperationVLANCreate, Class: pluginapi.ClassConfig,
			Parameters: parameters,
		})
		if !pluginapi.IsErrorCode(err, pluginapi.ErrorInvalidRequest) {
			t.Fatalf("parameters=%v error=%v", parameters, err)
		}
	}
}

func TestVLANResultRequiresStructuredOutput(t *testing.T) {
	plugin, _ := New(pluginapi.VendorHuawei)
	plan, err := plugin.BuildPlan(context.Background(), pluginapi.PlanRequest{
		PlanID: "plan", DeviceID: "device",
		Device: pluginapi.DeviceInfo{Vendor: pluginapi.VendorHuawei, Model: "FAKE-SW"},
		Operation: pluginapi.OperationVLANList, Class: pluginapi.ClassQuery,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	result, err := plugin.ParseResult(context.Background(), plan, pluginapi.Transcript{StartedAt: now, FinishedAt: now.Add(time.Millisecond), Commands: []pluginapi.CommandRecord{{Sequence: 1, Command: plan.Commands[0].Text, Output: `{"vlans":[{"vlan_id":100,"name":"office"}]}`, Succeeded: true}}})
	if err != nil || result.Status != pluginapi.ResultSuccess {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	_, err = plugin.ParseResult(context.Background(), plan, pluginapi.Transcript{StartedAt: now, FinishedAt: now.Add(time.Millisecond), Commands: []pluginapi.CommandRecord{{Sequence: 1, Command: plan.Commands[0].Text, Output: `{}`, Succeeded: true}}})
	if !pluginapi.IsErrorCode(err, pluginapi.ErrorOutputUnparsable) {
		t.Fatalf("error=%v", err)
	}
}
