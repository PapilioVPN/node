package accounting

import (
	"net/netip"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/engines/wireguard/stats/traffic"
)

func TestPeerActivityExpiresWithoutTrafficGrowth(t *testing.T) {
	service := New("wg-test", nil, true)
	key := [32]byte{1}
	endpoint := netip.MustParseAddr("2001:db8::1")
	old := time.Now().Add(-activeWindow - time.Second)
	rows := []traffic.PeerStats{{PublicKey: key, Counters: traffic.Counters{RXBytes: 10}, LastHandshake: old, EndpointIP: endpoint}}
	service.applySample(rows, old, false)
	rows[0].LastHandshake = time.Now()
	service.applySample(rows, time.Now(), false)
	got, err := service.PeerActivity(t.Context(), "wg-test", key[:])
	if err != nil || got.Active || !got.LastActive.Equal(old) || got.Endpoint != endpoint {
		t.Fatalf("expired activity = %+v, %v", got, err)
	}
	rows[0].RXBytes++
	service.applySample(rows, time.Now(), false)
	got, err = service.PeerActivity(t.Context(), "wg-test", key[:])
	if err != nil || !got.Active || got.Endpoint != endpoint {
		t.Fatalf("new activity = %+v, %v", got, err)
	}
}
