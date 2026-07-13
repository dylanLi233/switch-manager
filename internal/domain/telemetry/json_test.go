package telemetry

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEmptyTelemetryPagesMarshalArrays(t *testing.T) {
	for _, value := range []any{
		MACPage{Page: 2, PageSize: 50, Total: 0},
		ARPPage{Page: 2, PageSize: 50, Total: 0},
	} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(encoded), `"entries":[]`) || strings.Contains(string(encoded), `"entries":null`) {
			t.Fatalf("encoded=%s", encoded)
		}
	}
}

func TestMACEntryJSONRequiresAllFieldsAndRejectsUnknownFields(t *testing.T) {
	for _, encoded := range []string{
		`{"mac_address":"00:11:22:33:44:55","vlan_id":1,"interface_name":"FakeEthernet1/0/1","entry_type":"DYNAMIC"}`,
		`{"mac_address":"00:11:22:33:44:55","vlan_id":1,"interface_name":"FakeEthernet1/0/1","entry_type":"DYNAMIC","age_seconds":0,"vendor_field":true}`,
	} {
		var entry MACEntry
		if err := json.Unmarshal([]byte(encoded), &entry); err == nil {
			t.Fatalf("encoded=%s expected error", encoded)
		}
	}
	var valid MACEntry
	if err := json.Unmarshal([]byte(`{"mac_address":"00:11:22:33:44:55","vlan_id":1,"interface_name":"FakeEthernet1/0/1","entry_type":"DYNAMIC","age_seconds":0}`), &valid); err != nil {
		t.Fatal(err)
	}
}

func TestARPEntryJSONRequiresAgeAndSupportsIncompleteWithoutMAC(t *testing.T) {
	var missing ARPEntry
	if err := json.Unmarshal([]byte(`{"ip_address":"192.0.2.1","interface_name":"FakeEthernet1/0/1","state":"INCOMPLETE"}`), &missing); err == nil {
		t.Fatal("expected missing age_seconds error")
	}
	var valid ARPEntry
	if err := json.Unmarshal([]byte(`{"ip_address":"192.0.2.1","interface_name":"FakeEthernet1/0/1","state":"INCOMPLETE","age_seconds":0}`), &valid); err != nil {
		t.Fatal(err)
	}
	if valid.MACAddress != "" {
		t.Fatalf("entry=%+v", valid)
	}
}
