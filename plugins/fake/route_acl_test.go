package fake

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dylanLi233/switch-manager/internal/domain/acl"
	"github.com/dylanLi233/switch-manager/internal/domain/route"
	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
)

func routeACLRequest(name pluginapi.OperationName, class pluginapi.OperationClass, parameters map[string]any) pluginapi.PlanRequest {
	return pluginapi.PlanRequest{PlanID: "plan-route-acl", DeviceID: "device-route-acl", Device: pluginapi.DeviceInfo{Vendor: pluginapi.VendorHuawei, Model: "FAKE-SW"}, Operation: name, Class: class, Parameters: parameters}
}

func TestFakeRoutePlanIsStableAfterJSONRoundTrip(t *testing.T) {
	plugin, _ := New(pluginapi.VendorHuawei)
	spec := route.Spec{AddressFamily: route.FamilyIPv4, Destination: "192.0.2.7/24", NextHop: "198.51.100.1", OutgoingInterface: "FakeEthernet1/0/1"}
	first, err := plugin.BuildPlan(context.Background(), routeACLRequest(pluginapi.OperationRouteCreate, pluginapi.ClassConfig, map[string]any{"route": spec}))
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(map[string]any{"route": spec})
	var restored map[string]any
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	second, err := plugin.BuildPlan(context.Background(), routeACLRequest(pluginapi.OperationRouteCreate, pluginapi.ClassConfig, restored))
	if err != nil || first.Commands[0].Text != second.Commands[0].Text {
		t.Fatalf("first=%s second=%s err=%v", first.Commands[0].Text, second.Commands[0].Text, err)
	}
	if !strings.Contains(first.Commands[0].Text, `"destination":"192.0.2.0/24"`) {
		t.Fatalf("command=%s", first.Commands[0].Text)
	}
}

func TestFakeACLPlanUsesExplicitExperimentalDTO(t *testing.T) {
	plugin, _ := New(pluginapi.VendorHuawei)
	spec := acl.Spec{SchemaVersion: acl.ExperimentalSchemaVersion, Name: "FAKE_ACL_WEB", AddressFamily: acl.FamilyIPv4, Rules: []acl.Rule{{Sequence: 10, Action: acl.ActionPermit, Protocol: acl.ProtocolTCP, Source: "any", Destination: "192.0.2.9/24", DestinationPorts: []acl.PortRange{{From: 443, To: 443}}}}}
	plan, err := plugin.BuildPlan(context.Background(), routeACLRequest(pluginapi.OperationACLCreate, pluginapi.ClassConfig, map[string]any{"acl": spec}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.Commands[0].Text, `"schema_version":"experimental-v1"`) || !strings.Contains(plan.Commands[0].Text, `"destination":"192.0.2.0/24"`) {
		t.Fatalf("command=%s", plan.Commands[0].Text)
	}
}

func TestFakeRouteACLRejectsUnsafeAndUnknownFields(t *testing.T) {
	plugin, _ := New(pluginapi.VendorHuawei)
	tests := []pluginapi.PlanRequest{
		routeACLRequest(pluginapi.OperationRouteCreate, pluginapi.ClassConfig, map[string]any{"route": map[string]any{"address_family": "IPV4", "destination": "192.0.2.0/24", "next_hop": "198.51.100.1", "outgoing_interface": "FakeEthernet1/0/1\nshutdown"}}),
		routeACLRequest(pluginapi.OperationRouteCreate, pluginapi.ClassConfig, map[string]any{"route": map[string]any{"address_family": "IPV4", "destination": "192.0.2.0/24", "next_hop": "198.51.100.1", "unknown": true}}),
		routeACLRequest(pluginapi.OperationACLCreate, pluginapi.ClassConfig, map[string]any{"acl": map[string]any{"schema_version": "v1", "name": "FAKE_ACL_WEB", "address_family": "IPV4", "rules": []any{}}}),
		routeACLRequest(pluginapi.OperationACLCreate, pluginapi.ClassConfig, map[string]any{"acl": map[string]any{"schema_version": acl.ExperimentalSchemaVersion, "name": "acl permit any", "address_family": "IPV4", "rules": []any{map[string]any{"sequence": 10, "action": "PERMIT", "protocol": "ANY", "source": "any", "destination": "any"}}}}),
	}
	for _, request := range tests {
		if _, err := plugin.BuildPlan(context.Background(), request); err == nil {
			t.Fatalf("request=%+v expected error", request)
		}
	}
}

func TestFakeRouteACLParserRejectsVendorInvalidOutput(t *testing.T) {
	plugin, _ := New(pluginapi.VendorHuawei)
	plan, err := plugin.BuildPlan(context.Background(), routeACLRequest(pluginapi.OperationACLGet, pluginapi.ClassQuery, map[string]any{"acl_id": "acl-000001"}))
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC()
	output := `{"schema_version":"experimental-v1","acl":{"acl_id":"acl-000001","schema_version":"experimental-v1","name":"NOT_FAKE","address_family":"IPV4","rules":[{"sequence":10,"action":"PERMIT","protocol":"ANY","source":"any","destination":"any"}]}}`
	transcript := pluginapi.Transcript{StartedAt: started, FinishedAt: started.Add(time.Millisecond), Commands: []pluginapi.CommandRecord{{Sequence: 1, Command: plan.Commands[0].Text, Output: output, Succeeded: true, Duration: time.Millisecond}}}
	if _, err := plugin.ParseResult(context.Background(), plan, transcript); err == nil {
		t.Fatal("expected output validation error")
	}
}
