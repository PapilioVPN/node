package http

import (
	"testing"

	"github.com/PapilioVPN/node/internal/engines/xray/stats/traffic"
)

func TestScopeReportJSON(t *testing.T) {
	report := []traffic.CounterReset{
		{Name: "team>>>café\"\n", Direction: traffic.Uplink, DirectionReset: traffic.DirectionReset{
			Effect: traffic.ResetAcknowledged, PreviousBytes: -42,
		}},
		{Name: "alice", Direction: traffic.Downlink, DirectionReset: traffic.DirectionReset{Effect: traffic.ResetUnknown}},
	}
	want := `{"outcome":"unknown","counters":[{"name":"team\u003e\u003e\u003ecafé\"\n","direction":"uplink","result":{"effect":"acknowledged","previous_bytes":-42}},{"name":"alice","direction":"downlink","result":{"effect":"unknown"}}]}` + "\n"
	if got := string(appendScopeReport("unknown", report)); got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
	if got := string(appendScopeReport("complete", nil)); got != "{\"outcome\":\"complete\",\"counters\":[]}\n" {
		t.Fatalf("empty report: %s", got)
	}
}
