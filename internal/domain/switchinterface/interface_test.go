package switchinterface

import "testing"

func TestInterfaceValidation(t *testing.T) {
	t.Parallel()
	access := Interface{Name: "FakeEthernet1/0/1", AdminState: AdminEnabled, OperState: OperUp, Mode: ModeAccess, AccessVLAN: 100}
	if err := access.Validate(); err != nil {
		t.Fatal(err)
	}
	trunk := Interface{Name: "FakeEthernet1/0/2", AdminState: AdminEnabled, OperState: OperUp, Mode: ModeTrunk, NativeVLAN: 100, AllowedVLANs: []int{100, 200}}
	if err := trunk.Validate(); err != nil {
		t.Fatal(err)
	}
	trunk.NativeVLAN = 300
	if err := trunk.Validate(); err == nil {
		t.Fatal("expected native VLAN membership error")
	}
}

func TestNameSafetyDoesNotAssumeVendorSyntax(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"FakeEthernet1/0/1", "Port-A", "eth0.100", "接口一"} {
		if err := ValidateNameSafety(name); err != nil {
			t.Errorf("ValidateNameSafety(%q)=%v", name, err)
		}
	}
	for _, name := range []string{"", " port", "port\nshutdown", "port\x00x"} {
		if err := ValidateNameSafety(name); err == nil {
			t.Errorf("ValidateNameSafety(%q) expected error", name)
		}
	}
}

func TestNormalizeVLANsRejectsDuplicates(t *testing.T) {
	t.Parallel()
	if _, err := NormalizeVLANs([]int{100, 100}, true); err == nil {
		t.Fatal("expected duplicate error")
	}
	values, err := NormalizeVLANs([]int{200, 100}, true)
	if err != nil || values[0] != 100 || values[1] != 200 {
		t.Fatalf("values=%v err=%v", values, err)
	}
}
