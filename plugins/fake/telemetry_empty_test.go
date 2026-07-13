package fake

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dylanLi233/switch-manager/internal/domain/telemetry"
	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
)

func TestEmptyTelemetryTableReturnsSuccessfulEmptyPage(t *testing.T) {
	plugin, _ := New(pluginapi.VendorHuawei)
	plan := telemetryPlan(t, pluginapi.OperationMACTableList, map[string]any{"page": 4, "page_size": 50, "result_limit": 5000})
	result, err := plugin.ParseResult(context.Background(), plan, telemetryTranscript(plan, `{"entries":[]}`, false))
	if err != nil {
		t.Fatal(err)
	}
	page, ok := result.Data.(telemetry.MACPage)
	if !ok || result.Status != pluginapi.ResultSuccess || page.Total != 0 || page.Page != 4 || len(page.Entries) != 0 {
		t.Fatalf("result=%+v page=%+v", result, page)
	}
	encoded, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"entries":[]`) {
		t.Fatalf("encoded=%s", encoded)
	}
}
