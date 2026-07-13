package fakeruntime

import (
	"context"
	"testing"
	"time"

	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
	fakeplugin "github.com/dylanLi233/switch-manager/plugins/fake"
)

func TestDeviceStatusRuntimeOutputParsesThroughFakePlugin(t *testing.T) {
	runtime := New()
	session, err := runtime.Open(context.Background(), telemetryManagedDevice("device-bridge"))
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	plugin, err := fakeplugin.New(pluginapi.VendorHuawei)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := plugin.BuildPlan(context.Background(), pluginapi.PlanRequest{PlanID: "plan-status", DeviceID: "device-bridge", Device: pluginapi.DeviceInfo{Vendor: pluginapi.VendorHuawei, Model: "FAKE-SW"}, Operation: pluginapi.OperationDeviceStatusGet, Class: pluginapi.ClassQuery})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC()
	output, err := session.Execute(context.Background(), plan.Commands[0])
	if err != nil {
		t.Fatal(err)
	}
	transcript := pluginapi.Transcript{StartedAt: started, FinishedAt: time.Now().UTC(), Commands: []pluginapi.CommandRecord{{Sequence: 1, Command: plan.Commands[0].Text, Output: output.Output, Succeeded: true, Duration: output.Duration}}}
	result, err := plugin.ParseResult(context.Background(), plan, transcript)
	if err != nil {
		t.Fatalf("output=%s parse error=%v", output.Output, err)
	}
	if result.Status != pluginapi.ResultSuccess || result.Data == nil {
		t.Fatalf("result=%+v", result)
	}
}
