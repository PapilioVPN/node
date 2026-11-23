package native

import (
	"testing"

	"github.com/PapilioVPN/node/internal/engines/xray/stats/traffic"
)

func TestGroupTrafficPreservesExactNamesDirectionsAndScope(t *testing.T) {
	rows := []*TrafficCounter{
		{Name: "user>>>team>>>alice>>>traffic>>>uplink"},
		{Name: "user>>>alice>>>traffic>>>downlink", Value: 23},
		{Name: "inbound>>>shared>>>user>>>team>>>alice>>>traffic>>>downlink", Value: 123},
		{Name: "user>>>other>>>online>>>count", Value: 400},
		{Name: "user>>>>>>traffic>>>uplink", Value: 400},
		{Name: "user>>>team>>>alice>>>traffic>>>downlink", Value: 20},
	}
	got := groupTraffic("user>>>", rows)
	want := []traffic.Usage{
		{Name: "team>>>alice", DownlinkBytes: 20, HasUplink: true, HasDownlink: true},
		{Name: "alice", DownlinkBytes: 23, HasDownlink: true},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("scope result = %+v", got)
	}
}
