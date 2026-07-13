package fakeruntime

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/dylanLi233/switch-manager/internal/domain/device"
	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
)

func managedDevice() device.Device {
	now := time.Now().UTC()
	return device.Device{ID: "device", Name: "fake", Host: "192.0.2.1", SSHPort: 22, CredentialID: "credential", Vendor: device.VendorHuawei, Model: "FAKE-SW", DetectMode: device.DetectModeAuto, IdentityStatus: device.IdentityVerified, Status: device.StatusActive, CreatedAt: now, UpdatedAt: now}
}

func TestRuntimeVLANCRUD(t *testing.T) {
	factory := New()
	session, err := factory.Open(context.Background(), managedDevice())
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	for _, command := range []string{
		`fake.vlan.create {"vlan_id":100,"name":"office"}`,
		`fake.vlan.update {"vlan_id":100,"name":"staff"}`,
	} {
		if _, err := session.Execute(context.Background(), pluginapi.PlannedCommand{Sequence: 1, Text: command, Timeout: time.Second}); err != nil {
			t.Fatalf("command=%s err=%v", command, err)
		}
	}
	output, err := session.Execute(context.Background(), pluginapi.PlannedCommand{Sequence: 1, Text: "fake.vlan.list", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	var listed struct {
		VLANs []struct {
			ID   int    `json:"vlan_id"`
			Name string `json:"name"`
		} `json:"vlans"`
	}
	if err := json.Unmarshal([]byte(output.Output), &listed); err != nil || len(listed.VLANs) != 1 || listed.VLANs[0].Name != "staff" {
		t.Fatalf("output=%s listed=%+v err=%v", output.Output, listed, err)
	}
	if _, err := session.Execute(context.Background(), pluginapi.PlannedCommand{Sequence: 1, Text: `fake.vlan.delete {"vlan_id":100}`, Timeout: time.Second}); err != nil {
		t.Fatal(err)
	}
	if values := factory.Snapshot("device"); len(values) != 0 {
		t.Fatalf("values=%+v", values)
	}
}

func TestRuntimeRejectsDuplicateAndMissingVLAN(t *testing.T) {
	factory := New()
	session, _ := factory.Open(context.Background(), managedDevice())
	command := pluginapi.PlannedCommand{Sequence: 1, Text: `fake.vlan.create {"vlan_id":100,"name":"office"}`, Timeout: time.Second}
	if _, err := session.Execute(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Execute(context.Background(), command); !apperror.IsCode(err, apperror.CodeStateConflict) {
		t.Fatalf("duplicate error=%v", err)
	}
	if _, err := session.Execute(context.Background(), pluginapi.PlannedCommand{Sequence: 1, Text: `fake.vlan.get {"vlan_id":200}`, Timeout: time.Second}); !apperror.IsCode(err, apperror.CodeResourceNotFound) {
		t.Fatalf("missing error=%v", err)
	}
}
