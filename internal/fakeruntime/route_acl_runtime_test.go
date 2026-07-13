package fakeruntime

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/dylanLi233/switch-manager/internal/domain/acl"
	"github.com/dylanLi233/switch-manager/internal/domain/device"
	"github.com/dylanLi233/switch-manager/internal/domain/route"
	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
)

func openRouteACLSession(t *testing.T, factory *Factory, deviceID string) *session {
	t.Helper()
	opened, err := factory.Open(context.Background(), device.Device{ID: deviceID, Name: "fake", Host: "192.0.2.70", SSHPort: 22, CredentialID: "credential", Vendor: device.VendorHuawei, Model: "FAKE-SW", DetectMode: device.DetectModeAuto, IdentityStatus: device.IdentityVerified, Status: device.StatusActive})
	if err != nil {
		t.Fatal(err)
	}
	return opened.(*session)
}

func executeText(t *testing.T, session *session, text string) map[string]any {
	t.Helper()
	output, err := session.Execute(context.Background(), pluginapi.PlannedCommand{Sequence: 1, Text: text})
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal([]byte(output.Output), &object); err != nil {
		t.Fatal(err)
	}
	return object
}

func TestFakeRouteRuntimeCRUD(t *testing.T) {
	factory := New()
	session := openRouteACLSession(t, factory, "device-route")
	created := executeText(t, session, `fake.route.create {"route":{"address_family":"IPV4","destination":"192.0.2.0/24","next_hop":"198.51.100.1","outgoing_interface":"FakeEthernet1/0/1"}}`)
	routeObject := created["route"].(map[string]any)
	id := routeObject["route_id"].(string)
	if id != "route-000001" || len(factory.SnapshotRoutes("device-route")) != 1 {
		t.Fatalf("created=%v state=%v", created, factory.SnapshotRoutes("device-route"))
	}
	executeText(t, session, `fake.route.update {"route_id":"route-000001","route":{"address_family":"IPV4","destination":"203.0.113.0/24","next_hop":"198.51.100.1"}}`)
	state := factory.SnapshotRoutes("device-route")
	if len(state) != 1 || state[0].Destination != "203.0.113.0/24" {
		t.Fatalf("state=%+v", state)
	}
	executeText(t, session, `fake.route.delete {"route_id":"route-000001"}`)
	if len(factory.SnapshotRoutes("device-route")) != 0 {
		t.Fatal("route was not deleted")
	}
}

func TestFakeACLRuntimeCRUDAndSchema(t *testing.T) {
	factory := New()
	session := openRouteACLSession(t, factory, "device-acl")
	command := `fake.acl.create {"acl":{"schema_version":"experimental-v1","name":"FAKE_ACL_WEB","address_family":"IPV4","rules":[{"sequence":10,"action":"PERMIT","protocol":"TCP","source":"any","destination":"192.0.2.0/24","destination_ports":[{"from":443,"to":443}]}]}}`
	created := executeText(t, session, command)
	if created["schema_version"] != acl.ExperimentalSchemaVersion {
		t.Fatalf("created=%v", created)
	}
	state := factory.SnapshotACLs("device-acl")
	if len(state) != 1 || state[0].ACLID != "acl-000001" || state[0].Name != "FAKE_ACL_WEB" {
		t.Fatalf("state=%+v", state)
	}
	executeText(t, session, `fake.acl.update {"acl_id":"acl-000001","acl":{"schema_version":"experimental-v1","name":"FAKE_ACL_WEB","address_family":"IPV4","rules":[{"sequence":10,"action":"DENY","protocol":"ANY","source":"any","destination":"any"}]}}`)
	if factory.SnapshotACLs("device-acl")[0].Rules[0].Action != acl.ActionDeny {
		t.Fatal("ACL was not updated")
	}
	executeText(t, session, `fake.acl.delete {"acl_id":"acl-000001"}`)
	if len(factory.SnapshotACLs("device-acl")) != 0 {
		t.Fatal("ACL was not deleted")
	}
}

func TestFakeRouteRejectsMissingOutgoingInterfaceAndDuplicate(t *testing.T) {
	factory := New()
	session := openRouteACLSession(t, factory, "device-route-conflict")
	if _, err := session.Execute(context.Background(), pluginapi.PlannedCommand{Sequence: 1, Text: `fake.route.create {"route":{"address_family":"IPV4","destination":"192.0.2.0/24","next_hop":"198.51.100.1","outgoing_interface":"FakeEthernet9/9/9"}}`}); err == nil {
		t.Fatal("expected missing interface error")
	}
	command := `fake.route.create {"route":{"address_family":"IPV4","destination":"192.0.2.0/24","next_hop":"198.51.100.1"}}`
	if _, err := session.Execute(context.Background(), pluginapi.PlannedCommand{Sequence: 1, Text: command}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Execute(context.Background(), pluginapi.PlannedCommand{Sequence: 1, Text: command}); err == nil {
		t.Fatal("expected duplicate route conflict")
	}
}

func TestRuntimeSnapshotsAreIsolated(t *testing.T) {
	factory := New()
	first := openRouteACLSession(t, factory, "device-one")
	_ = openRouteACLSession(t, factory, "device-two")
	executeText(t, first, `fake.route.create {"route":{"address_family":"IPV6","destination":"2001:db8::/64","next_hop":"2001:db8::1"}}`)
	if len(factory.SnapshotRoutes("device-one")) != 1 || len(factory.SnapshotRoutes("device-two")) != 0 {
		t.Fatal("device route state leaked")
	}
}

var _ = route.FamilyIPv4
