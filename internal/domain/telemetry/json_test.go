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
