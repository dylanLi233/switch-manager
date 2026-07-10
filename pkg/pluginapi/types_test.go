package pluginapi

import (
	"testing"
	"time"
)

func validMetadata() Metadata {
	return Metadata{
		Name:          "fake-huawei",
		Vendor:        VendorHuawei,
		PluginVersion: Version{Major: 1, Minor: 0, Patch: 0},
		SDKVersion:    CurrentSDKVersion(),
		Operations:    []OperationName{"diagnostic.echo", "configuration.echo"},
	}
}

func TestMetadataValidation(t *testing.T) {
	t.Parallel()
	if err := validMetadata().Validate(CurrentSDKVersion()); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	duplicate := validMetadata()
	duplicate.Operations = []OperationName{"diagnostic.echo", "diagnostic.echo"}
	if err := duplicate.Validate(CurrentSDKVersion()); err == nil {
		t.Fatal("expected duplicate operation error")
	}
	incompatible := validMetadata()
	incompatible.SDKVersion = Version{Major: 2, Minor: 0, Patch: 0}
	if err := incompatible.Validate(CurrentSDKVersion()); err == nil {
		t.Fatal("expected incompatible SDK error")
	}
}

func TestCapabilitySet(t *testing.T) {
	t.Parallel()
	set, err := NewCapabilitySet(
		Capability{Operation: "diagnostic.echo", Level: SupportSupported},
		Capability{Operation: "configuration.echo", Level: SupportUnsupported, Reason: "unknown model"},
	)
	if err != nil {
		t.Fatal(err)
	}
	capability, ok := set.Lookup("configuration.echo")
	if !ok || capability.Level != SupportUnsupported {
		t.Fatalf("Lookup() = %+v, %v", capability, ok)
	}
	if err := set.ValidateAgainst(validMetadata()); err != nil {
		t.Fatalf("ValidateAgainst() error = %v", err)
	}
	undeclared, err := NewCapabilitySet(Capability{Operation: "route.list", Level: SupportSupported})
	if err != nil {
		t.Fatal(err)
	}
	if err := undeclared.ValidateAgainst(validMetadata()); err == nil {
		t.Fatal("expected undeclared capability error")
	}
}

func TestMetadataCloneDoesNotShareOperations(t *testing.T) {
	t.Parallel()
	original := validMetadata()
	clone := original.Clone()
	original.Operations[0] = "changed.operation"
	if clone.Operations[0] != "diagnostic.echo" {
		t.Fatal("clone shares operation slice")
	}
}

func testTime() time.Time { return time.Unix(100, 0).UTC() }

func TestPartialTranscriptMustEndInFailure(t *testing.T) {
	t.Parallel()
	plan := ExecutionPlan{
		PlanID: "p", DeviceID: "d", Vendor: VendorHuawei,
		PluginName: "fake-huawei", PluginVersion: "1.0.0",
		Operation: "diagnostic.echo", Class: ClassQuery, RiskLevel: RiskLow,
		Commands: []PlannedCommand{{Sequence: 1, Text: "one", Timeout: 1}, {Sequence: 2, Text: "two", Timeout: 1}},
	}
	transcript := Transcript{StartedAt: testTime(), FinishedAt: testTime().Add(1), Commands: []CommandRecord{{Sequence: 1, Command: "one", Succeeded: true}}}
	if err := transcript.ValidateAgainst(plan); err == nil {
		t.Fatal("expected successful partial transcript to fail")
	}
}

func TestPlanRejectsMultilineCommandAndNonJSONParameters(t *testing.T) {
	t.Parallel()
	plan := ExecutionPlan{
		PlanID: "p", DeviceID: "d", Vendor: VendorHuawei,
		PluginName: "fake-huawei", PluginVersion: "1.0.0",
		Operation: "diagnostic.echo", Class: ClassQuery, RiskLevel: RiskLow,
		Commands: []PlannedCommand{{Sequence: 1, Text: "one\ntwo", Timeout: time.Second}},
	}
	if err := plan.Validate(); err == nil {
		t.Fatal("expected multiline command rejection")
	}
	request := PlanRequest{
		PlanID: "p", DeviceID: "d", Device: DeviceInfo{Vendor: VendorHuawei},
		Operation: "diagnostic.echo", Class: ClassQuery,
		Parameters: map[string]any{"bad": func() {}},
	}
	if err := request.Validate(); err == nil {
		t.Fatal("expected non-JSON parameter rejection")
	}
}

func TestResultRejectsNonJSONData(t *testing.T) {
	t.Parallel()
	result := OperationResult{
		Status: ResultSuccess, Data: map[string]any{"bad": make(chan int)},
		StartedAt: testTime(), FinishedAt: testTime().Add(time.Second),
	}
	if err := result.Validate(); err == nil {
		t.Fatal("expected non-JSON result data rejection")
	}
}
