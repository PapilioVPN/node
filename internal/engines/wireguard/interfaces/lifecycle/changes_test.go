package lifecycle

import "testing"

func TestLiveFieldsIncludesCombinedChanges(t *testing.T) {
	before, err := parseConfig([]byte(`{"private_key":"AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=","addresses":["192.0.2.1/24"],"mtu":1400,"listen_port":51820}`))
	if err != nil {
		t.Fatal(err)
	}
	after := before
	after.MTU = 1300
	after.ListenPort = 51821
	checkpoint := Checkpoint{MTU: before.MTU, Fixed: before.fixed()}
	if got := checkpoint.liveField(after); got != LiveMTU|LivePort {
		t.Fatalf("combined MTU and port change classified as %v", got)
	}
}
