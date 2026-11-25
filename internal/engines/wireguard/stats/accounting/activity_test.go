package accounting

import (
	"net/netip"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/engines/wireguard/stats/traffic"
)

func TestPeerActivityExpiresWithoutTrafficGrowth(t *testing.T) {
	service := New("wg-test", nil, true, false)
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

func TestPeerActivityReadsDuringUsageWrite(t *testing.T) {
	service := New("wg-test", nil, true, false)
	key := [32]byte{1}
	at := time.Now()
	service.applySample([]traffic.PeerStats{{
		PublicKey: key, LastHandshake: at, Counters: traffic.Counters{RXBytes: 1},
	}}, at, false)
	service.mu.Lock()
	done := make(chan Activity, 1)
	go func() {
		got, _ := service.PeerActivity(t.Context(), "wg-test", key[:])
		done <- got
	}()
	select {
	case got := <-done:
		service.mu.Unlock()
		if !got.Active || !got.LastActive.Equal(at) {
			t.Fatalf("unexpected activity: %+v", got)
		}
	case <-time.After(time.Second):
		service.mu.Unlock()
		<-done
		t.Fatal("peer activity blocked on the usage ledger")
	}
}
