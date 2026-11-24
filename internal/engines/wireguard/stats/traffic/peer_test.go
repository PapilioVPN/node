package traffic

import (
	"context"
	"errors"
	"testing"

	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/status"
)

type peerReaderFunc func(context.Context, string, [32]byte) (PeerTrafficSample, error)

func (read peerReaderFunc) ReadPeerTraffic(ctx context.Context, name string, key [32]byte) (PeerTrafficSample, error) {
	return read(ctx, name, key)
}

func (peerReaderFunc) ReadPeerStats(context.Context, string) ([]PeerStats, error) {
	panic("single-peer traffic must use its focused reader")
}

func TestPeerTrafficResetLifetime(t *testing.T) {
	const name = "wg-test"
	key := [32]byte{1}
	sample := PeerTrafficSample{Counters: Counters{RXBytes: 100, TXBytes: 70}, LinkIndex: 1}
	service := New(name, nil, peerReaderFunc(func(context.Context, string, [32]byte) (PeerTrafficSample, error) {
		return sample, nil
	}), true)
	reset := func(want Counters) {
		t.Helper()
		if got, err := service.ResetPeerTraffic(t.Context(), name, key[:]); err != nil || got != want {
			t.Fatalf("reset = %+v, %v, want %+v", got, err, want)
		}
	}
	read := func(want Counters) {
		t.Helper()
		if got, err := service.GetPeerTraffic(t.Context(), name, key[:]); err != nil || got != want {
			t.Fatalf("read = %+v, %v, want %+v", got, err, want)
		}
	}
	reset(sample.Counters)
	read(Counters{})
	sample.Counters = Counters{RXBytes: 200, TXBytes: 80}
	read(Counters{RXBytes: 100, TXBytes: 10})
	// Counter rollback can still leave the current values above the baseline.
	sample.Counters = Counters{RXBytes: 150, TXBytes: 75}
	read(sample.Counters)
	reset(sample.Counters)
	service.ForgetPeerTraffic(key)
	// A replacement may already have exceeded the previous peer's counters.
	sample.Counters = Counters{RXBytes: 300, TXBytes: 210}
	read(sample.Counters)
	reset(sample.Counters)
	sample.LinkIndex++
	read(sample.Counters)
}

func TestPeerTrafficResetRejectsInvalidatedSample(t *testing.T) {
	const name = "wg-test"
	key := [32]byte{1}
	sample := PeerTrafficSample{Counters: Counters{RXBytes: 100, TXBytes: 70}, LinkIndex: 1}
	var service *Service
	service = New(name, nil, peerReaderFunc(func(context.Context, string, [32]byte) (PeerTrafficSample, error) {
		service.ForgetPeerTraffic(key)
		return sample, nil
	}), true)
	if _, err := service.ResetPeerTraffic(t.Context(), name, key[:]); !errors.Is(err, status.ErrBusy) {
		t.Fatalf("invalidated reset returned %v", err)
	}
	if len(service.resets) != 0 {
		t.Fatal("invalidated sample changed the baseline")
	}
}

func TestPeerTrafficResetRequiresManagedMode(t *testing.T) {
	service := New("wg-test", nil, nil, false)
	key := [32]byte{1}
	if _, err := service.ResetPeerTraffic(t.Context(), "wg-test", key[:]); !errors.Is(err, ErrResetDisabled) {
		t.Fatalf("read-only reset returned %v", err)
	}
}
