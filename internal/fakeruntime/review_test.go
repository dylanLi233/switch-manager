package fakeruntime

import (
	"context"
	"testing"
	"time"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
)

func TestRuntimeRejectsTrailingJSON(t *testing.T) {
	factory := New()
	session, err := factory.Open(context.Background(), managedDevice())
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	_, err = session.Execute(context.Background(), pluginapi.PlannedCommand{
		Sequence: 1,
		Text: `fake.vlan.create {"vlan_id":100,"name":"office"} {"vlan_id":200}`,
		Timeout: time.Second,
	})
	if !apperror.IsCode(err, apperror.CodeCommandRejected) {
		t.Fatalf("error=%v", err)
	}
	if values := factory.Snapshot("device"); len(values) != 0 {
		t.Fatalf("state changed after rejected command: %+v", values)
	}
}
