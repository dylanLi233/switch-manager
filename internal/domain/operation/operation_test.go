package operation

import (
	"testing"
	"time"

	"github.com/dylanLi233/switch-manager/internal/domain/auth"
	"github.com/dylanLi233/switch-manager/internal/domain/device"
)

func validActor() auth.Actor {
	return auth.Actor{UserID: "user-1", Username: "alice", Role: auth.RoleAdmin}
}

func TestRequestValidate(t *testing.T) {
	t.Parallel()
	valid := Request{
		Name: "vlan.create", Class: ClassConfig, DeviceID: "sw-1",
		ExecutionMode: ExecutionModeSync, Actor: validActor(),
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	queryWithSave := valid
	queryWithSave.Class = ClassQuery
	queryWithSave.SaveConfig = true
	if err := queryWithSave.Validate(); err == nil {
		t.Fatal("expected save_config validation error")
	}

	backupSync := valid
	backupSync.Name = "config.backup"
	backupSync.Class = ClassBackup
	backupSync.ExecutionMode = ExecutionModeSync
	if err := backupSync.Validate(); err == nil {
		t.Fatal("expected asynchronous backup validation error")
	}
}

func TestExecutionPlanValidate(t *testing.T) {
	t.Parallel()
	valid := ExecutionPlan{
		PlanID: "plan-1", DeviceID: "sw-1", Vendor: device.VendorHuawei,
		PluginName: "huawei", PluginVersion: "1.0.0", Operation: "vlan.create",
		Class: ClassConfig, RiskLevel: RiskMedium,
		Commands: []PlannedCommand{{Sequence: 1, Text: "verified-command", Timeout: time.Second}},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	badSequence := valid
	badSequence.Commands = []PlannedCommand{{Sequence: 2, Text: "verified-command", Timeout: time.Second}}
	if err := badSequence.Validate(); err == nil {
		t.Fatal("expected command sequence error")
	}

	badTimeout := valid
	badTimeout.Commands[0].Timeout = 0
	if err := badTimeout.Validate(); err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestResultValidate(t *testing.T) {
	t.Parallel()
	start := time.Now().UTC()
	success := Result{Status: ResultSuccess, StartedAt: start, FinishedAt: start.Add(time.Second)}
	if err := success.Validate(); err != nil {
		t.Fatalf("success Validate() error = %v", err)
	}

	success.ErrorCode = "SHOULD_NOT_EXIST"
	if err := success.Validate(); err == nil {
		t.Fatal("expected successful result error invariant")
	}

	failed := Result{Status: ResultFailed, StartedAt: start, FinishedAt: start.Add(time.Second)}
	if err := failed.Validate(); err == nil {
		t.Fatal("expected failed result to require error code")
	}

	partial := Result{
		Status: ResultPartialSuccess, ErrorCode: "CONFIG_SAVE_FAILED",
		StartedAt: start, FinishedAt: start.Add(time.Second),
		Commands: []CommandExecution{
			{Sequence: 1, Succeeded: true, Duration: time.Millisecond},
			{Sequence: 2, Succeeded: false, ErrorCode: "CONFIG_SAVE_FAILED", Duration: time.Millisecond},
		},
	}
	if err := partial.Validate(); err != nil {
		t.Fatalf("partial Validate() error = %v", err)
	}
}
