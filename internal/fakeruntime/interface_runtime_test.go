package fakeruntime

import (
	"context"
	"testing"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/dylanLi233/switch-manager/internal/domain/device"
	"github.com/dylanLi233/switch-manager/internal/domain/switchinterface"
	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
)

func openInterfaceSession(t *testing.T, runtime *Factory, deviceID string) *session {
	t.Helper()
	opened, err := runtime.Open(context.Background(), device.Device{ID: deviceID, Name: "fake", Host: "192.0.2.70", SSHPort: 22, CredentialID: "credential", Vendor: device.VendorHuawei, Model: "FAKE-SW", DetectMode: device.DetectModeAuto, IdentityStatus: device.IdentityVerified, Status: device.StatusActive})
	if err != nil {
		t.Fatal(err)
	}
	return opened.(*session)
}

func executeInterface(t *testing.T, session *session, text string) (string, error) {
	t.Helper()
	output, err := session.Execute(context.Background(), pluginapi.PlannedCommand{Sequence: 1, Text: text})
	return output.Output, err
}

func TestFakeInterfaceRuntimeStateTransitions(t *testing.T) {
	runtime := New()
	session := openInterfaceSession(t, runtime, "device-interface")
	if state := runtime.SnapshotInterfaces("device-interface"); len(state) != 2 {
		t.Fatalf("initial state=%+v", state)
	}
	if _, err := executeInterface(t, session, `fake.interface.disable {"interface_name":"FakeEthernet1/0/1"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := executeInterface(t, session, `fake.interface.access {"interface_name":"FakeEthernet1/0/1","vlan_id":100}`); err != nil {
		t.Fatal(err)
	}
	if _, err := executeInterface(t, session, `fake.interface.trunk {"interface_name":"FakeEthernet1/0/2","allowed_vlans":[100,200],"native_vlan":100}`); err != nil {
		t.Fatal(err)
	}
	if _, err := executeInterface(t, session, `fake.interface.vlan.add {"interface_name":"FakeEthernet1/0/2","vlan_id":300}`); err != nil {
		t.Fatal(err)
	}
	if _, err := executeInterface(t, session, `fake.interface.vlan.remove {"interface_name":"FakeEthernet1/0/2","vlan_id":200}`); err != nil {
		t.Fatal(err)
	}
	state := runtime.SnapshotInterfaces("device-interface")
	if state[0].Name != "FakeEthernet1/0/1" || state[0].AdminState != switchinterface.AdminDisabled || state[0].AccessVLAN != 100 {
		t.Fatalf("access=%+v", state[0])
	}
	if state[1].Mode != switchinterface.ModeTrunk || len(state[1].AllowedVLANs) != 2 || state[1].AllowedVLANs[0] != 100 || state[1].AllowedVLANs[1] != 300 {
		t.Fatalf("trunk=%+v", state[1])
	}
}

func TestFakeInterfaceRuntimeRejectsUnsafeOrInvalidTransitions(t *testing.T) {
	runtime := New()
	session := openInterfaceSession(t, runtime, "device-interface-errors")
	before := runtime.SnapshotInterfaces("device-interface-errors")
	cases := []string{
		`fake.interface.get {"interface_name":"FakeEthernet1/0/1"}{"interface_name":"FakeEthernet1/0/2"}`,
		"fake.interface.get {\"interface_name\":\"FakeEthernet1/0/1\\nshutdown\"}",
		`fake.interface.vlan.add {"interface_name":"FakeEthernet1/0/1","vlan_id":100}`,
		`fake.interface.vlan.remove {"interface_name":"FakeEthernet1/0/2","vlan_id":1}`,
	}
	for _, command := range cases {
		if _, err := executeInterface(t, session, command); err == nil {
			t.Fatalf("command=%s expected error", command)
		}
	}
	after := runtime.SnapshotInterfaces("device-interface-errors")
	if len(before) != len(after) || after[0].AccessVLAN != before[0].AccessVLAN || after[1].NativeVLAN != before[1].NativeVLAN {
		t.Fatalf("state changed before=%+v after=%+v", before, after)
	}
	if _, err := executeInterface(t, session, `fake.interface.get {"interface_name":"FakeEthernet9/9/9"}`); !apperror.IsCode(err, apperror.CodeResourceNotFound) {
		t.Fatalf("error=%v", err)
	}
}
