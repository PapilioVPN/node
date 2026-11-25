package accounting

import (
	"context"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/engines/wireguard/stats/traffic"
)

type readerFunc func(context.Context, string) ([]traffic.PeerStats, error)

func (read readerFunc) ReadPeerStats(ctx context.Context, name string) ([]traffic.PeerStats, error) {
	return read(ctx, name)
}

func (read readerFunc) ReadPeerStatsForKey(ctx context.Context, name string, key [32]byte) (traffic.PeerStats, error) {
	rows, err := read(ctx, name)
	if err != nil {
		return traffic.PeerStats{}, err
	}
	for _, row := range rows {
		if row.PublicKey == key {
			return row, nil
		}
	}
	return traffic.PeerStats{}, traffic.ErrPeerNotFound
}

func TestSampledPeriodsAcrossRemovalAndReplacement(t *testing.T) {
	now := time.Now()
	first, second := [32]byte{1}, [32]byte{2}
	rows := []traffic.PeerStats{
		{PublicKey: first, Counters: traffic.Counters{RXBytes: 100, TXBytes: 70}, LastHandshake: now},
		{PublicKey: second, Counters: traffic.Counters{RXBytes: 19, TXBytes: 5}, LastHandshake: now},
	}
	service := New("wg-test", readerFunc(func(context.Context, string) ([]traffic.PeerStats, error) { return rows, nil }), true)
	service.applySample(rows, now, false)
	read, err := service.ReadPeer(t.Context(), "wg-test", first[:], true)
	if err != nil || read.Peer.Counters != rows[0].Counters {
		t.Fatalf("initial reset = %+v, %v", read, err)
	}
	rows[0].RXBytes += 30
	rows[0].TXBytes += 20
	rows[1].RXBytes += 15
	if err := service.WithPeerRemoval(t.Context(), [][32]byte{first}, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	after, err := service.ReadPeer(t.Context(), "wg-test", second[:], false)
	if err != nil || after.Peer.RXBytes != 19 || !after.SampledAt.Equal(now) {
		t.Fatalf("single removal changed the full sample: %+v, %v", after, err)
	}
	rows[0].Counters = traffic.Counters{RXBytes: 300, TXBytes: 400}
	service.applySample(rows, now, false)
	usage, err := service.ReadAll(t.Context(), "wg-test", true)
	if err != nil || len(usage.Peers) != 2 {
		t.Fatalf("group reset = %+v, %v", usage, err)
	}
	byKey := make(map[[32]byte]traffic.Counters)
	for _, peer := range usage.Peers {
		byKey[peer.PublicKey] = peer.Counters
	}
	if byKey[first] != (traffic.Counters{RXBytes: 330, TXBytes: 420}) || byKey[second] != rows[1].Counters {
		t.Fatalf("combined periods = %+v", byKey)
	}
	if got, err := service.ReadAll(t.Context(), "wg-test", false); err != nil || len(got.Peers) != 0 {
		t.Fatalf("usage after reset = %+v, %v", got, err)
	}
}
