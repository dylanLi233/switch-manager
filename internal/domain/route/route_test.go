package route

import "testing"

func TestNormalizeSpecCanonicalizesPrefix(t *testing.T) {
	value, err := NormalizeSpec(Spec{AddressFamily: FamilyIPv4, Destination: "192.0.2.7/24", NextHop: "198.51.100.1", OutgoingInterface: "FakeEthernet1/0/1", Description: "office route"})
	if err != nil {
		t.Fatal(err)
	}
	if value.Destination != "192.0.2.0/24" || value.NextHop != "198.51.100.1" {
		t.Fatalf("value=%+v", value)
	}
}

func TestNormalizeSpecRejectsFamilyMismatchAndUnsafeInterface(t *testing.T) {
	for _, value := range []Spec{
		{AddressFamily: FamilyIPv4, Destination: "192.0.2.0/24", NextHop: "2001:db8::1"},
		{AddressFamily: FamilyIPv6, Destination: "2001:db8::/64", NextHop: "192.0.2.1"},
		{AddressFamily: FamilyIPv4, Destination: "192.0.2.0/24", NextHop: "198.51.100.1", OutgoingInterface: "port\nshutdown"},
	} {
		if _, err := NormalizeSpec(value); err == nil {
			t.Fatalf("value=%+v expected error", value)
		}
	}
}

func TestRouteIDIsURLSafe(t *testing.T) {
	for _, value := range []string{"", " route-1", "route/1", "route\\1", "route\n1"} {
		if err := ValidateID(value); err == nil {
			t.Errorf("ValidateID(%q) expected error", value)
		}
	}
	if err := ValidateID("route-000001"); err != nil {
		t.Fatal(err)
	}
}
