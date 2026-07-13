package acl

import "testing"

func TestNormalizeExperimentalACL(t *testing.T) {
	value, err := NormalizeSpec(Spec{
		SchemaVersion: ExperimentalSchemaVersion,
		Name:          "FAKE_ACL_WEB",
		AddressFamily: FamilyIPv4,
		Rules: []Rule{
			{Sequence: 20, Action: ActionDeny, Protocol: ProtocolAny, Source: "any", Destination: "203.0.113.9/24"},
			{Sequence: 10, Action: ActionPermit, Protocol: ProtocolTCP, Source: "192.0.2.9/24", Destination: "198.51.100.4/24", DestinationPorts: []PortRange{{From: 443, To: 443}, {From: 80, To: 80}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if value.Rules[0].Sequence != 10 || value.Rules[0].Source != "192.0.2.0/24" || value.Rules[0].Destination != "198.51.100.0/24" || value.Rules[0].DestinationPorts[0].From != 80 {
		t.Fatalf("value=%+v", value)
	}
	if value.Rules[1].Destination != "203.0.113.0/24" {
		t.Fatalf("destination=%s", value.Rules[1].Destination)
	}
}

func TestACLRejectsWrongSchemaAndInvalidPorts(t *testing.T) {
	for _, value := range []Spec{
		{SchemaVersion: "v1", Name: "FAKE_ACL", AddressFamily: FamilyIPv4, Rules: []Rule{{Sequence: 10, Action: ActionPermit, Protocol: ProtocolAny, Source: "any", Destination: "any"}}},
		{SchemaVersion: ExperimentalSchemaVersion, Name: "FAKE_ACL", AddressFamily: FamilyIPv4, Rules: []Rule{{Sequence: 10, Action: ActionPermit, Protocol: ProtocolICMP, Source: "any", Destination: "any", DestinationPorts: []PortRange{{From: 80, To: 80}}}}},
		{SchemaVersion: ExperimentalSchemaVersion, Name: "FAKE_ACL", AddressFamily: FamilyIPv4, Rules: []Rule{{Sequence: 10, Action: ActionPermit, Protocol: ProtocolTCP, Source: "any", Destination: "any", DestinationPorts: []PortRange{{From: 80, To: 100}, {From: 90, To: 110}}}}},
	} {
		if _, err := NormalizeSpec(value); err == nil {
			t.Fatalf("value=%+v expected error", value)
		}
	}
}

func TestACLNameSafetyDoesNotPretendVendorSyntax(t *testing.T) {
	if err := ValidateNameSafety("vendor-neutral name"); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"", " acl", "acl\npermit", "acl\x00name"} {
		if err := ValidateNameSafety(value); err == nil {
			t.Errorf("ValidateNameSafety(%q) expected error", value)
		}
	}
}
