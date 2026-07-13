package fake

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
)

func fakeInterfaceRequest(operation pluginapi.OperationName, class pluginapi.OperationClass, parameters map[string]any) pluginapi.PlanRequest {
	return pluginapi.PlanRequest{PlanID: "plan-interface", DeviceID: "device-interface", Device: pluginapi.DeviceInfo{Vendor: pluginapi.VendorHuawei, Model: "FAKE-SW"}, Operation: operation, Class: class, Parameters: parameters}
}

func TestFakeInterfaceNameValidatorOwnsSyntax(t *testing.T) {
	plugin, _ := New(pluginapi.VendorHuawei)
	if err := plugin.ValidateInterfaceName("FakeEthernet1/0/1"); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"GigabitEthernet1/0/1", "FakeEthernet1/0/1\nshutdown", "FakeEthernet1/0/1;delete", "../FakeEthernet1/0/1", "FakeEthernet0/0/1"} {
		if err := plugin.ValidateInterfaceName(value); err == nil {
			t.Errorf("ValidateInterfaceName(%q) expected error", value)
		}
	}
}

func TestFakeInterfacePlansUseJSONAndStableVLANArrays(t *testing.T) {
	plugin, _ := New(pluginapi.VendorHuawei)
	plan, err := plugin.BuildPlan(context.Background(), fakeInterfaceRequest(pluginapi.OperationInterfaceTrunk, pluginapi.ClassConfig, map[string]any{"interface_name": "FakeEthernet1/0/2", "allowed_vlans": []int{200, 100}, "native_vlan": 100}))
	if err != nil {
		t.Fatal(err)
	}
	want := `fake.interface.trunk {"interface_name":"FakeEthernet1/0/2","allowed_vlans":[100,200],"native_vlan":100}`
	if plan.Commands[0].Text != want {
		t.Fatalf("command=%s want=%s", plan.Commands[0].Text, want)
	}
	rebuilt, err := plugin.BuildPlan(context.Background(), fakeInterfaceRequest(pluginapi.OperationInterfaceTrunk, pluginapi.ClassConfig, map[string]any{"interface_name": "FakeEthernet1/0/2", "allowed_vlans": []any{float64(200), float64(100)}, "native_vlan": float64(100)}))
	if err != nil || rebuilt.Commands[0].Text != want {
		t.Fatalf("rebuilt=%+v err=%v", rebuilt, err)
	}
}

func TestFakeInterfacePlanRejectsInjectionAndUnknownParameters(t *testing.T) {
	plugin, _ := New(pluginapi.VendorHuawei)
	for _, parameters := range []map[string]any{
		{"interface_name": "FakeEthernet1/0/1\nshutdown"},
		{"interface_name": "FakeEthernet1/0/1", "unexpected": true},
		{"interface_name": "FakeEthernet1/0/2", "allowed_vlans": []int{100, 100}},
		{"interface_name": "FakeEthernet1/0/2", "allowed_vlans": []int{100}, "native_vlan": 200},
	} {
		operation := pluginapi.OperationInterfaceGet
		class := pluginapi.ClassQuery
		if _, exists := parameters["allowed_vlans"]; exists {
			operation, class = pluginapi.OperationInterfaceTrunk, pluginapi.ClassConfig
		}
		if _, err := plugin.BuildPlan(context.Background(), fakeInterfaceRequest(operation, class, parameters)); err == nil {
			t.Fatalf("parameters=%v expected error", parameters)
		}
	}
}

func TestFakeInterfaceParserRejectsInvalidOutput(t *testing.T) {
	plugin, _ := New(pluginapi.VendorHuawei)
	plan, err := plugin.BuildPlan(context.Background(), fakeInterfaceRequest(pluginapi.OperationInterfaceGet, pluginapi.ClassQuery, map[string]any{"interface_name": "FakeEthernet1/0/1"}))
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC()
	transcript := pluginapi.Transcript{
		StartedAt:  started,
		FinishedAt: started.Add(time.Millisecond),
		Commands: []pluginapi.CommandRecord{{
			Sequence: 1, Command: plan.Commands[0].Text,
			Output: `{"interface":{"name":"FakeEthernet1/0/1"}}`,
			Succeeded: true, Duration: time.Millisecond,
		}},
	}
	if _, err := plugin.ParseResult(context.Background(), plan, transcript); err == nil || !strings.Contains(err.Error(), "invalid schema") {
		t.Fatalf("error=%v", err)
	}
}
