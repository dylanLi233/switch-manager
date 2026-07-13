package fakeruntime

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/dylanLi233/switch-manager/internal/domain/device"
	"github.com/dylanLi233/switch-manager/internal/domain/telemetry"
	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
)

func telemetryManagedDevice(id string) device.Device {
	now := time.Now().UTC()
	return device.Device{ID: id, Name: "fake", Host: "192.0.2.80", SSHPort: 22, CredentialID: "credential-id", Vendor: device.VendorHuawei, Model: "FAKE-SW", DetectMode: device.DetectModeAuto, IdentityStatus: device.IdentityVerified, Status: device.StatusActive, CreatedAt: now, UpdatedAt: now}
}

func TestTelemetryRuntimeReturnsFullTablesAndIsolatesDevices(t *testing.T) {
	runtime := New()
	first, err := runtime.Open(context.Background(), telemetryManagedDevice("device-a"))
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := runtime.Open(context.Background(), telemetryManagedDevice("device-b"))
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	entries := []telemetry.MACEntry{{MACAddress: "00:11:22:33:44:99", VLANID: 100, InterfaceName: "FakeEthernet1/0/1", EntryType: telemetry.MACDynamic, AgeSeconds: 1}}
	if err := runtime.ReplaceMACEntries("device-a", entries); err != nil {
		t.Fatal(err)
	}
	output, err := first.Execute(context.Background(), pluginapi.PlannedCommand{Sequence: 1, Text: `fake.mac_table.list {"page":1,"page_size":1,"result_limit":5000}`, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Entries []telemetry.MACEntry `json:"entries"`
	}
	if err := json.Unmarshal([]byte(output.Output), &payload); err != nil || len(payload.Entries) != 1 || payload.Entries[0].VLANID != 100 {
		t.Fatalf("payload=%+v err=%v", payload, err)
	}
	if got := runtime.SnapshotMACEntries("device-b"); len(got) != 2 {
		t.Fatalf("device-b entries=%+v", got)
	}
	payload.Entries[0].VLANID = 200
	if got := runtime.SnapshotMACEntries("device-a"); len(got) != 1 || got[0].VLANID != 100 {
		t.Fatalf("runtime state mutated through output: %+v", got)
	}
}

func TestTelemetryRuntimeStatusReturnsFreshCollectionTimeAndDeepCopy(t *testing.T) {
	runtime := New()
	session, err := runtime.Open(context.Background(), telemetryManagedDevice("device-status"))
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	cpu := 10.0
	status := telemetry.DeviceStatus{Hostname: "status-switch", UptimeSeconds: 50, HealthState: telemetry.HealthDegraded, CPUUsagePercent: &cpu, ActiveAlarms: []string{"fan warning"}, CollectedAt: time.Now().Add(-time.Hour)}
	if err := runtime.ReplaceStatus("device-status", status); err != nil {
		t.Fatal(err)
	}
	before := time.Now().UTC()
	output, err := session.Execute(context.Background(), pluginapi.PlannedCommand{Sequence: 1, Text: "fake.device_status.get", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	var returned telemetry.DeviceStatus
	if err := json.Unmarshal([]byte(output.Output), &returned); err != nil {
		t.Fatal(err)
	}
	if returned.CollectedAt.Before(before) || returned.Hostname != "status-switch" || len(returned.ActiveAlarms) != 1 {
		t.Fatalf("returned=%+v", returned)
	}
	returned.ActiveAlarms[0] = "mutated"
	if got := runtime.SnapshotStatus("device-status"); got.ActiveAlarms[0] != "fan warning" {
		t.Fatalf("status state mutated through snapshot: %+v", got)
	}
}
