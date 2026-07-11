package fakecli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
)

func TestInjectedFailurePreservesPartialOutput(t *testing.T) {
	t.Parallel()
	command := pluginapi.PlannedCommand{Sequence: 1, Text: "fake.fail", Timeout: time.Second}
	session, err := New([]Step{{
		Expect: command, Output: "partial device output",
		Failure: errors.New("rejected"), ErrorCode: "COMMAND_REJECTED",
	}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	output, err := session.Execute(context.Background(), command)
	if !IsKind(err, ErrorInjectedFailure) {
		t.Fatalf("Execute() error=%v", err)
	}
	if output.Output != "partial device output" {
		t.Fatalf("output=%+v", output)
	}
	records := session.Records()
	if len(records) != 1 || records[0].Output != "partial device output" {
		t.Fatalf("records=%+v", records)
	}
}

func TestSensitiveUnexpectedCommandIsRedacted(t *testing.T) {
	t.Parallel()
	expected := pluginapi.PlannedCommand{
		Sequence: 1, Text: "fake.secret expected-password", Sensitive: true, Timeout: time.Second,
	}
	received := pluginapi.PlannedCommand{
		Sequence: 1, Text: "fake.secret received-password", Sensitive: true, Timeout: time.Second,
	}
	session, err := New([]Step{{Expect: expected}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = session.Execute(context.Background(), received)
	if !IsKind(err, ErrorUnexpectedCommand) {
		t.Fatalf("Execute() error=%v", err)
	}
	message := err.Error()
	if strings.Contains(message, "expected-password") || strings.Contains(message, "received-password") {
		t.Fatalf("sensitive command leaked in error: %s", message)
	}
	if !strings.Contains(message, "<redacted>") {
		t.Fatalf("redaction marker missing: %s", message)
	}
}
