package fakecli

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
	fakeplugin "github.com/dylanLi233/switch-manager/plugins/fake"
)

func planned(sequence int, text string, timeout time.Duration) pluginapi.PlannedCommand {
	return pluginapi.PlannedCommand{Sequence: sequence, Text: text, Timeout: timeout}
}

func waitUntilInUse(t *testing.T, session *Session) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !session.inUse.Load() || session.Remaining() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("session did not enter scripted execution")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestSessionSuccessTranscriptAndCheckpoint(t *testing.T) {
	t.Parallel()
	detect := planned(1, "fake.detect", time.Second)
	query := planned(1, "fake.echo.query \"hello\"", 2*time.Second)
	session, err := New([]Step{
		{Expect: detect, Output: "detected"},
		{Expect: query, Output: "hello"},
	}, Options{})
	if err != nil {
		t.Fatal(err)
	}

	if output, err := session.Execute(context.Background(), detect); err != nil || output.Output != "detected" {
		t.Fatalf("detect output=%+v err=%v", output, err)
	}
	checkpoint := session.Checkpoint()
	if checkpoint != 1 {
		t.Fatalf("checkpoint=%d", checkpoint)
	}
	if output, err := session.Execute(context.Background(), query); err != nil || output.Output != "hello" {
		t.Fatalf("query output=%+v err=%v", output, err)
	}

	transcript, err := session.TranscriptSince(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	if len(transcript.Commands) != 1 || transcript.Commands[0].Command != query.Text || !transcript.Commands[0].Succeeded {
		t.Fatalf("transcript=%+v", transcript)
	}
	if err := session.AssertComplete(); err != nil {
		t.Fatalf("AssertComplete() error=%v", err)
	}
}

func TestUnexpectedCommandIsViolationAndDoesNotConsume(t *testing.T) {
	t.Parallel()
	expected := planned(1, "fake.detect", time.Second)
	session, err := New([]Step{{Expect: expected}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = session.Execute(context.Background(), planned(1, "wrong.command", time.Second))
	if !IsKind(err, ErrorUnexpectedCommand) {
		t.Fatalf("Execute() error=%v", err)
	}
	if session.Remaining() != 1 {
		t.Fatalf("remaining=%d", session.Remaining())
	}
	if err := session.AssertComplete(); !IsKind(err, ErrorScriptIncomplete) {
		t.Fatalf("AssertComplete() error=%v", err)
	}
}

func TestScriptExhausted(t *testing.T) {
	t.Parallel()
	session, err := New(nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = session.Execute(context.Background(), planned(1, "fake.detect", time.Second))
	if !IsKind(err, ErrorScriptExhausted) {
		t.Fatalf("Execute() error=%v", err)
	}
	if err := session.AssertComplete(); !IsKind(err, ErrorScriptIncomplete) {
		t.Fatalf("AssertComplete() error=%v", err)
	}
}

func TestInjectedFailurePreservesCauseAndTranscriptCode(t *testing.T) {
	t.Parallel()
	cause := errors.New("device rejected input")
	command := planned(1, "fake.echo.config \"bad\"", time.Second)
	session, err := New([]Step{{
		Expect: command, Failure: cause, ErrorCode: "COMMAND_REJECTED",
	}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = session.Execute(context.Background(), command)
	if !IsKind(err, ErrorInjectedFailure) || !errors.Is(err, cause) {
		t.Fatalf("Execute() error=%v", err)
	}
	records := session.Records()
	if len(records) != 1 || records[0].Succeeded || records[0].ErrorCode != "COMMAND_REJECTED" {
		t.Fatalf("records=%+v", records)
	}
	if err := session.AssertComplete(); err != nil {
		t.Fatal(err)
	}
}

func TestDisconnectClosesSession(t *testing.T) {
	t.Parallel()
	command := planned(1, "fake.detect", time.Second)
	session, err := New([]Step{{Expect: command, Disconnect: true}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = session.Execute(context.Background(), command)
	if !IsKind(err, ErrorSessionClosed) {
		t.Fatalf("disconnect error=%v", err)
	}
	_, err = session.Execute(context.Background(), command)
	if !IsKind(err, ErrorSessionClosed) {
		t.Fatalf("second error=%v", err)
	}
	records := session.Records()
	if len(records) != 1 || records[0].ErrorCode != "DEVICE_UNREACHABLE" {
		t.Fatalf("records=%+v", records)
	}
}

func TestCommandTimeout(t *testing.T) {
	t.Parallel()
	command := planned(1, "fake.slow", 5*time.Millisecond)
	session, err := New([]Step{{Expect: command, Delay: 100 * time.Millisecond}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = session.Execute(context.Background(), command)
	if !IsKind(err, ErrorCommandTimeout) {
		t.Fatalf("Execute() error=%v", err)
	}
	records := session.Records()
	if len(records) != 1 || records[0].ErrorCode != "COMMAND_TIMEOUT" {
		t.Fatalf("records=%+v", records)
	}
}

func TestContextCancellationDuringCommand(t *testing.T) {
	t.Parallel()
	command := planned(1, "fake.wait", time.Second)
	session, err := New([]Step{{Expect: command, Delay: time.Second}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, executeErr := session.Execute(ctx, command)
		result <- executeErr
	}()
	waitUntilInUse(t, session)
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Execute() error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled execution did not return")
	}
	records := session.Records()
	if len(records) != 1 || records[0].ErrorCode != "CONTEXT_CANCELLED" {
		t.Fatalf("records=%+v", records)
	}
}

func TestOutputTruncation(t *testing.T) {
	t.Parallel()
	command := planned(1, "fake.large", time.Second)
	session, err := New([]Step{{Expect: command, Output: "abcdefgh"}}, Options{MaxOutputBytes: 4})
	if err != nil {
		t.Fatal(err)
	}
	output, err := session.Execute(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if output.Output != "abcd" || !output.OutputTruncated {
		t.Fatalf("output=%+v", output)
	}
	records := session.Records()
	if len(records) != 1 || records[0].Output != "abcd" || !records[0].OutputTruncated {
		t.Fatalf("records=%+v", records)
	}
}

func TestConcurrentUseIsRejectedAndRecordedAsViolation(t *testing.T) {
	t.Parallel()
	command := planned(1, "fake.wait", time.Second)
	session, err := New([]Step{{Expect: command, Delay: 50 * time.Millisecond}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	first := make(chan error, 1)
	go func() {
		_, executeErr := session.Execute(context.Background(), command)
		first <- executeErr
	}()
	waitUntilInUse(t, session)
	if _, err := session.Execute(context.Background(), command); !IsKind(err, ErrorConcurrentUse) {
		t.Fatalf("concurrent Execute() error=%v", err)
	}
	if err := <-first; err != nil {
		t.Fatalf("first Execute() error=%v", err)
	}
	if err := session.AssertComplete(); !IsKind(err, ErrorScriptIncomplete) {
		t.Fatalf("AssertComplete() error=%v", err)
	}
}

func TestAssertCompleteDetectsUnusedSteps(t *testing.T) {
	t.Parallel()
	session, err := New([]Step{{Expect: planned(1, "fake.detect", time.Second)}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.AssertComplete(); !IsKind(err, ErrorScriptIncomplete) {
		t.Fatalf("AssertComplete() error=%v", err)
	}
}

func TestNewRejectsInvalidScript(t *testing.T) {
	t.Parallel()
	if _, err := New([]Step{{Expect: planned(1, "bad\ncommand", time.Second)}}, Options{}); !IsKind(err, ErrorInvalidScript) {
		t.Fatalf("newline script error=%v", err)
	}
	if _, err := New(nil, Options{MaxOutputBytes: -1}); !IsKind(err, ErrorInvalidScript) {
		t.Fatalf("negative output limit error=%v", err)
	}
	if _, err := New([]Step{{
		Expect: planned(1, "fake.detect", time.Second), ErrorCode: "COMMAND_REJECTED",
	}}, Options{}); !IsKind(err, ErrorInvalidScript) {
		t.Fatalf("orphan error code error=%v", err)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	t.Parallel()
	command := planned(1, "fake.detect", time.Second)
	session, err := New([]Step{{Expect: command}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	session.Close()
	session.Close()
	if _, err := session.Execute(context.Background(), command); !IsKind(err, ErrorSessionClosed) {
		t.Fatalf("Execute() error=%v", err)
	}
}

func TestFakePluginEndToEndWithOneSession(t *testing.T) {
	t.Parallel()
	plugin, err := fakeplugin.New(pluginapi.VendorHuawei)
	if err != nil {
		t.Fatal(err)
	}
	detectCommand := planned(1, "fake.detect", time.Second)
	configCommand := planned(1, "fake.echo.config \"hello\"", 2*time.Second)
	session, err := New([]Step{
		{Expect: detectCommand, Output: "vendor=HUAWEI;model=FAKE-SW;os=1.0;prompt=fake"},
		{Expect: configCommand, Output: "hello"},
	}, Options{})
	if err != nil {
		t.Fatal(err)
	}

	info, err := plugin.Detect(context.Background(), session)
	if err != nil {
		t.Fatalf("Detect() error=%v", err)
	}
	checkpoint := session.Checkpoint()
	plan, err := plugin.BuildPlan(context.Background(), pluginapi.PlanRequest{
		PlanID: "plan-1", DeviceID: "device-1", Device: info,
		Operation: fakeplugin.OperationEchoConfig, Class: pluginapi.ClassConfig,
		Parameters: map[string]any{"message": "hello"}, SaveConfig: true,
	})
	if err != nil {
		t.Fatalf("BuildPlan() error=%v", err)
	}
	if len(plan.Commands) != 1 || plan.Commands[0] != configCommand {
		t.Fatalf("plan command=%+v", plan.Commands)
	}
	if _, err := session.Execute(context.Background(), plan.Commands[0]); err != nil {
		t.Fatalf("Execute() error=%v", err)
	}
	transcript, err := session.TranscriptSince(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	result, err := plugin.ParseResult(context.Background(), plan, transcript)
	if err != nil {
		t.Fatalf("ParseResult() error=%v", err)
	}
	if result.Status != pluginapi.ResultSuccess {
		t.Fatalf("result=%+v", result)
	}
	if err := session.AssertComplete(); err != nil {
		t.Fatalf("AssertComplete() error=%v", err)
	}
}
