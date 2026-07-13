package fake

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/dylanLi233/switch-manager/internal/domain/telemetry"
	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
)

func telemetryPlan(t *testing.T, operation pluginapi.OperationName, parameters map[string]any) pluginapi.ExecutionPlan {
	t.Helper()
	plugin, err := New(pluginapi.VendorHuawei)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := plugin.BuildPlan(context.Background(), pluginapi.PlanRequest{PlanID: "plan-telemetry", DeviceID: "device-telemetry", Device: pluginapi.DeviceInfo{Vendor: pluginapi.VendorHuawei, Model: "FAKE-SW"}, Operation: operation, Class: pluginapi.ClassQuery, Parameters: parameters})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func telemetryTranscript(plan pluginapi.ExecutionPlan, output string, truncated bool) pluginapi.Transcript {
	started := time.Now().UTC()
	return pluginapi.Transcript{StartedAt: started, FinishedAt: started.Add(time.Millisecond), Commands: []pluginapi.CommandRecord{{Sequence: 1, Command: plan.Commands[0].Text, Output: output, Succeeded: true, OutputTruncated: truncated, Duration: time.Millisecond}}}
}

func TestTelemetryPlanIsStableAfterJSONRoundTrip(t *testing.T) {
	plan := telemetryPlan(t, pluginapi.OperationMACTableList, map[string]any{"page": 2, "page_size": 50, "result_limit": 5000})
	want := `fake.mac_table.list {"page":2,"page_size":50,"result_limit":5000}`
	if plan.Commands[0].Text != want {
		t.Fatalf("command=%s want=%s", plan.Commands[0].Text, want)
	}
	rebuilt := telemetryPlan(t, pluginapi.OperationMACTableList, map[string]any{"page": float64(2), "page_size": float64(50), "result_limit": float64(5000)})
	if rebuilt.Commands[0].Text != want {
		t.Fatalf("rebuilt=%s want=%s", rebuilt.Commands[0].Text, want)
	}
}

func TestMACParserPaginatesAfterFullValidation(t *testing.T) {
	plugin, _ := New(pluginapi.VendorHuawei)
	plan := telemetryPlan(t, pluginapi.OperationMACTableList, map[string]any{"page": 2, "page_size": 2, "result_limit": 10})
	output := `{"entries":[{"mac_address":"00:11:22:33:44:77","vlan_id":2,"interface_name":"FakeEthernet1/0/2","entry_type":"DYNAMIC","age_seconds":3},{"mac_address":"00:11:22:33:44:55","vlan_id":1,"interface_name":"FakeEthernet1/0/1","entry_type":"DYNAMIC","age_seconds":1},{"mac_address":"00:11:22:33:44:66","vlan_id":1,"interface_name":"FakeEthernet1/0/2","entry_type":"STATIC","age_seconds":0}]}`
	result, err := plugin.ParseResult(context.Background(), plan, telemetryTranscript(plan, output, false))
	if err != nil {
		t.Fatal(err)
	}
	page, ok := result.Data.(telemetry.MACPage)
	if !ok || page.Total != 3 || page.Page != 2 || len(page.Entries) != 1 || page.Entries[0].VLANID != 2 {
		t.Fatalf("page=%T %+v", result.Data, result.Data)
	}
}

func TestTelemetryResultLimitReturnsFailedResult(t *testing.T) {
	plugin, _ := New(pluginapi.VendorHuawei)
	plan := telemetryPlan(t, pluginapi.OperationARPTableList, map[string]any{"page": 1, "page_size": 1, "result_limit": 1})
	output := `{"entries":[{"ip_address":"192.0.2.1","mac_address":"00:11:22:33:44:55","interface_name":"FakeEthernet1/0/1","state":"REACHABLE","age_seconds":1},{"ip_address":"192.0.2.2","mac_address":"00:11:22:33:44:66","interface_name":"FakeEthernet1/0/2","state":"STALE","age_seconds":2}]}`
	result, err := plugin.ParseResult(context.Background(), plan, telemetryTranscript(plan, output, false))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != pluginapi.ResultFailed || result.ErrorCode != string(pluginapi.ErrorResultTooLarge) || result.Data != nil {
		t.Fatalf("result=%+v", result)
	}
}

func TestTelemetryParserRejectsIncompleteOrTruncatedOutput(t *testing.T) {
	plugin, _ := New(pluginapi.VendorHuawei)
	plan := telemetryPlan(t, pluginapi.OperationMACTableList, map[string]any{"page": 1, "page_size": 50, "result_limit": 5000})
	for _, transcript := range []pluginapi.Transcript{
		telemetryTranscript(plan, `{}`, false),
		telemetryTranscript(plan, `{"entries":[]}`, true),
		telemetryTranscript(plan, `{"entries":[]} {"entries":[]}`, false),
	} {
		if _, err := plugin.ParseResult(context.Background(), plan, transcript); err == nil || !pluginapi.IsErrorCode(err, pluginapi.ErrorOutputUnparsable) {
			t.Fatalf("error=%v", err)
		}
	}
}

func TestDeviceStatusParserRequiresCompleteSchema(t *testing.T) {
	plugin, _ := New(pluginapi.VendorHuawei)
	plan := telemetryPlan(t, pluginapi.OperationDeviceStatusGet, nil)
	if _, err := plugin.ParseResult(context.Background(), plan, telemetryTranscript(plan, `{"hostname":"fake","uptime_seconds":1,"health_state":"HEALTHY","collected_at":"2026-07-13T00:00:00Z"}`, false)); err == nil {
		t.Fatal("expected missing active_alarms error")
	}
	valid := map[string]any{"hostname": "fake", "uptime_seconds": 1, "health_state": "HEALTHY", "active_alarms": []string{}, "collected_at": "2026-07-13T00:00:00Z"}
	encoded, _ := json.Marshal(valid)
	result, err := plugin.ParseResult(context.Background(), plan, telemetryTranscript(plan, string(encoded), false))
	if err != nil || result.Status != pluginapi.ResultSuccess {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
