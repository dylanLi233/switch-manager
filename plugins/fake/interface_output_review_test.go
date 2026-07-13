package fake

import (
	"context"
	"testing"
	"time"

	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
)

func TestFakeInterfaceParserRejectsWrongNativeVLANTypeAndForeignName(t *testing.T) {
	plugin, _ := New(pluginapi.VendorHuawei)
	plan, err := plugin.BuildPlan(context.Background(), fakeInterfaceRequest(pluginapi.OperationInterfaceGet, pluginapi.ClassQuery, map[string]any{"interface_name": "FakeEthernet1/0/2"}))
	if err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{
		`{"interface":{"name":"FakeEthernet1/0/2","admin_state":"ENABLED","oper_state":"UP","mode":"TRUNK","allowed_vlans":[100],"native_vlan":"100"}}`,
		`{"interface":{"name":"GigabitEthernet1/0/2","admin_state":"ENABLED","oper_state":"UP","mode":"TRUNK","allowed_vlans":[100],"native_vlan":100}}`,
	} {
		started := time.Now().UTC()
		transcript := pluginapi.Transcript{StartedAt: started, FinishedAt: started.Add(time.Millisecond), Commands: []pluginapi.CommandRecord{{Sequence: 1, Command: plan.Commands[0].Text, Output: output, Succeeded: true, Duration: time.Millisecond}}}
		if _, err := plugin.ParseResult(context.Background(), plan, transcript); err == nil {
			t.Fatalf("output=%s expected error", output)
		}
	}
}
