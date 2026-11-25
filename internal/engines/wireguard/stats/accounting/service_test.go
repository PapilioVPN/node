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
	service := New("wg-test", readerFunc(func(context.Context, string) ([]traffic.PeerStats, error) { return rows, nil }), true, false)
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

func TestSampledRemovalDoesNotWaitForOrPublishStaleSample(t *testing.T) {
	key := [32]byte{1}
	at := time.Now()
	initial := traffic.PeerStats{PublicKey: key, LastHandshake: at, Counters: traffic.Counters{RXBytes: 14, TXBytes: 8}}
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	service := New("wg-test", readerFunc(func(ctx context.Context, _ string) ([]traffic.PeerStats, error) {
		close(started)
		select {
		case <-release:
			newer := initial
			newer.RXBytes += 5
			return []traffic.PeerStats{newer}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}), true, true)
	service.applySample([]traffic.PeerStats{initial}, at, false)
	if !service.gate.TryAcquire(1) {
		t.Fatal("sampler could not enter")
	}
	go func() {
		defer close(finished)
		defer service.gate.Release(1)
		service.capture(t.Context(), false)
	}()
	defer func() { <-finished }()
	<-started

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	removed := false
	err := service.WithPeerRemoval(ctx, [][32]byte{key}, func() error {
		removed = true
		return nil
	})
	close(release)
	<-finished
	if err != nil || !removed {
		t.Fatalf("sampled removal waited for the sampler: removed %t, error %v", removed, err)
	}
	if usage, err := service.ReadPeer(t.Context(), "wg-test", key[:], false); err != nil ||
		usage.Peer.Counters != initial.Counters || !usage.SampledAt.Equal(at) {
		t.Fatalf("stale sample replaced the retained usage: %+v, %v", usage, err)
	}
	if activity, err := service.PeerActivity(t.Context(), "wg-test", key[:]); err != nil || activity.Active {
		t.Fatalf("stale sample reported the removed peer active: %+v, %v", activity, err)
	}
}
