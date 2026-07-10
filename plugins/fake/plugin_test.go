package fake

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
)

type sessionStub struct {
	output pluginapi.CommandOutput
	err    error
	seen   pluginapi.PlannedCommand
}

func (s *sessionStub) Execute(_ context.Context, command pluginapi.PlannedCommand) (pluginapi.CommandOutput, error) {
	s.seen = command
	return s.output, s.err
}

func TestFakePluginDetectBuildAndParse(t *testing.T) {
	t.Parallel()
	plugin, err := New(pluginapi.VendorHuawei)
	if err != nil {
		t.Fatal(err)
	}
	session := &sessionStub{output: pluginapi.CommandOutput{Output: "vendor=HUAWEI;model=FAKE-SW;os=1.0;prompt=fake"}}
	info, err := plugin.Detect(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	if session.seen.Text != "fake.detect" || info.Model != "FAKE-SW" {
		t.Fatalf("seen=%+v info=%+v", session.seen, info)
	}

	plan, err := plugin.BuildPlan(context.Background(), pluginapi.PlanRequest{
		PlanID: "plan-1", DeviceID: "device-1", Device: info,
		Operation: OperationEchoConfig, Class: pluginapi.ClassConfig,
		Parameters: map[string]any{"message": "hello"}, SaveConfig: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.EnterConfigMode || plan.Commands[0].Text == "" {
		t.Fatalf("plan = %+v", plan)
	}

	started := time.Now().UTC()
	result, err := plugin.ParseResult(context.Background(), plan, pluginapi.Transcript{
		StartedAt: started, FinishedAt: started.Add(time.Second),
		Commands: []pluginapi.CommandRecord{{
			Sequence: 1, Command: plan.Commands[0].Text, Output: "hello",
			Succeeded: true, Duration: time.Millisecond,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != pluginapi.ResultSuccess {
		t.Fatalf("result = %+v", result)
	}
}

func TestFakePluginUnknownModelBlocksConfig(t *testing.T) {
	t.Parallel()
	plugin, _ := New(pluginapi.VendorH3C)
	_, err := plugin.BuildPlan(context.Background(), pluginapi.PlanRequest{
		PlanID: "plan-1", DeviceID: "device-1",
		Device: pluginapi.DeviceInfo{Vendor: pluginapi.VendorH3C, Model: "UNKNOWN"},
		Operation: OperationEchoConfig, Class: pluginapi.ClassConfig,
		Parameters: map[string]any{"message": "hello"},
	})
	if !pluginapi.IsErrorCode(err, pluginapi.ErrorUnsupportedOperation) {
		t.Fatalf("BuildPlan() error = %v", err)
	}
}

func TestFakePluginDetectionFailurePreservesCause(t *testing.T) {
	t.Parallel()
	plugin, _ := New(pluginapi.VendorHuawei)
	cause := errors.New("session closed")
	_, err := plugin.Detect(context.Background(), &sessionStub{err: cause})
	if !pluginapi.IsErrorCode(err, pluginapi.ErrorDetectionFailed) || !errors.Is(err, cause) {
		t.Fatalf("Detect() error = %v", err)
	}
	if err.Error() == cause.Error() {
		t.Fatal("plugin error leaked raw cause as its public message")
	}
}
