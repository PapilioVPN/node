// Package accounting owns sampled WireGuard peer usage periods.
package accounting

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/peers"
	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/status"
	"github.com/PapilioVPN/node/internal/engines/wireguard/stats/traffic"
	"golang.org/x/sync/semaphore"
)

const (
	SampleInterval = 10 * time.Second
	activeWindow   = 45 * time.Second
	maxEntries     = 2 * peers.MaxPeers
)

type Reader interface {
	ReadPeerStats(context.Context, string) ([]traffic.PeerStats, error)
	ReadPeerStatsForKey(context.Context, string, [32]byte) (traffic.PeerStats, error)
}

type Usage struct {
	PublicKey [32]byte
	traffic.Counters
}

type Sample struct {
	SampledAt  time.Time
	Incomplete bool
}

type Single struct {
	Sample
	Peer Usage
}

type Collection struct {
	Sample
	Peers []Usage
}

type entry struct {
	last    traffic.Counters
	pending traffic.Counters
	seen    bool
	retired bool
}

type Service struct {
	name            string
	reader          Reader
	enabled         bool
	sampledRemovals bool
	gate            *semaphore.Weighted
	mu              sync.RWMutex
	entries         map[[32]byte]entry
	activity        sync.Map // [32]byte -> Activity, published under mu.
	sample          Sample
	sampleFence     uint64 // Protected by mu. Odd while a sampled removal is in flight.
	cancel          context.CancelFunc
	done            chan struct{}
}

func New(name string, reader Reader, enabled, sampledRemovals bool) *Service {
	return &Service{
		name: name, reader: reader, enabled: enabled, sampledRemovals: sampledRemovals,
		gate: semaphore.NewWeighted(1), entries: make(map[[32]byte]entry),
	}
}

func (service *Service) Start(context.Context) error {
	// The sampler owns a process-lifetime context and is joined during shutdown.
	ctx, cancel := context.WithCancel(context.Background())
	service.cancel, service.done = cancel, make(chan struct{})
	go func() {
		defer close(service.done)
		ticker := time.NewTicker(SampleInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if service.gate.TryAcquire(1) {
					service.capture(ctx, false)
					service.gate.Release(1)
				}
			}
		}
	}()
	return nil
}

func (service *Service) Stop(ctx context.Context) error {
	service.cancel()
	select {
	case <-service.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// WithPeerRemoval preserves pending usage. The lifecycle caller serializes
// removals with each other and with interface replacement.
func (service *Service) WithPeerRemoval(ctx context.Context, keys [][32]byte, remove func() error) error {
	if service.sampledRemovals {
		return service.removeWithSampledUsage(ctx, keys, remove)
	}
	return service.withRemoval(ctx, keys, false, true, remove)
}

func (service *Service) removeWithSampledUsage(ctx context.Context, keys [][32]byte, remove func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	service.mu.Lock()
	service.sampleFence++
	service.mu.Unlock()

	err := remove()

	service.mu.Lock()
	defer service.mu.Unlock()
	service.sampleFence++
	if err == nil {
		for _, key := range keys {
			service.retire(key, service.entries[key])
		}
	}
	return err
}

func (service *Service) WithLinkRemoval(ctx context.Context, captureFinal bool, remove func() error) error {
	return service.withRemoval(ctx, nil, true, captureFinal, remove)
}

func (service *Service) withRemoval(ctx context.Context, keys [][32]byte, all, captureFinal bool, remove func() error) error {
	if err := service.gate.Acquire(ctx, 1); err != nil {
		return err
	}
	defer service.gate.Release(1)
	if captureFinal {
		if !all && len(keys) == 1 {
			service.capturePeer(ctx, keys[0])
		} else {
			service.capture(ctx, true)
		}
	}
	if err := remove(); err != nil {
		return err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if all {
		for key, previous := range service.entries {
			service.retire(key, previous)
		}
	} else {
		for _, key := range keys {
			service.retire(key, service.entries[key])
		}
	}
	return nil
}

func (service *Service) capturePeer(ctx context.Context, key [32]byte) {
	ctx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	row, err := service.reader.ReadPeerStatsForKey(ctx, service.name, key)
	if err != nil || row.LastHandshake.IsZero() {
		return
	}
	at := time.Now().UTC()
	service.mu.Lock()
	defer service.mu.Unlock()
	previous, exists := service.entries[key]
	if !exists && len(service.entries) >= maxEntries {
		service.sample.Incomplete = true
		return
	}
	service.entries[key] = service.accumulate(previous, row, at, at.Add(-activeWindow))
}

func (service *Service) capture(ctx context.Context, includeInactive bool) {
	service.mu.RLock()
	fence := service.sampleFence
	service.mu.RUnlock()
	if fence%2 != 0 {
		return
	}
	// A failed native read leaves the last successful sample available.
	ctx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	rows, err := service.reader.ReadPeerStats(ctx, service.name)
	if err != nil {
		return
	}
	at := time.Now().UTC()
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.sampleFence == fence {
		service.applySample(rows, at, includeInactive)
	}
}

// applySample updates the ledger while mu is held.
func (service *Service) applySample(rows []traffic.PeerStats, at time.Time, includeInactive bool) {
	for key, previous := range service.entries {
		previous.seen = false
		service.entries[key] = previous
	}
	cutoff := at.Add(-activeWindow)
	service.sample = Sample{SampledAt: at}
	for _, row := range rows {
		previous, exists := service.entries[row.PublicKey]
		active := !row.LastHandshake.IsZero() && (includeInactive || !row.LastHandshake.Before(cutoff))
		if !active && !exists {
			continue
		}
		if !exists && len(service.entries) >= maxEntries {
			service.sample.Incomplete = true
			continue
		}
		previous.seen, previous.retired = true, false
		if active {
			previous = service.accumulate(previous, row, at, cutoff)
		}
		service.entries[row.PublicKey] = previous
	}
	for key, previous := range service.entries {
		if !previous.seen {
			service.retire(key, previous)
		}
	}
}

func (service *Service) accumulate(previous entry, row traffic.PeerStats, at, cutoff time.Time) entry {
	if row.RXBytes < previous.last.RXBytes || row.TXBytes < previous.last.TXBytes {
		previous.last = traffic.Counters{}
	}
	service.publishActivity(row, at, row.Counters != previous.last && !row.LastHandshake.Before(cutoff))
	previous.retired = false
	previous.pending.RXBytes = addBytes(previous.pending.RXBytes, row.RXBytes-previous.last.RXBytes)
	previous.pending.TXBytes = addBytes(previous.pending.TXBytes, row.TXBytes-previous.last.TXBytes)
	previous.last = row.Counters
	return previous
}

func addBytes(total, delta uint64) uint64 {
	if delta > math.MaxUint64-total {
		return math.MaxUint64
	}
	return total + delta
}

func (service *Service) retire(key [32]byte, previous entry) {
	service.activity.Delete(key)
	if previous.pending == (traffic.Counters{}) {
		delete(service.entries, key)
		return
	}
	previous.last = traffic.Counters{}
	previous.retired = true
	service.entries[key] = previous
}

func (service *Service) ReadPeer(ctx context.Context, name string, input []byte, reset bool) (Single, error) {
	if len(input) != 32 || [32]byte(input) == [32]byte{} {
		return Single{}, traffic.ErrInvalidPeerKey
	}
	if err := service.check(ctx, name); err != nil {
		return Single{}, err
	}
	key := [32]byte(input)
	if reset {
		service.mu.Lock()
		defer service.mu.Unlock()
	} else {
		service.mu.RLock()
		defer service.mu.RUnlock()
	}
	if err := ctx.Err(); err != nil {
		return Single{}, err
	}
	previous := service.entries[key]
	if reset {
		service.consume(key, previous)
	}
	return Single{Sample: service.sample, Peer: Usage{PublicKey: key, Counters: previous.pending}}, nil
}

func (service *Service) ReadAll(ctx context.Context, name string, reset bool) (Collection, error) {
	if err := service.check(ctx, name); err != nil {
		return Collection{}, err
	}
	if reset {
		service.mu.Lock()
		defer service.mu.Unlock()
	} else {
		service.mu.RLock()
		defer service.mu.RUnlock()
	}
	if err := ctx.Err(); err != nil {
		return Collection{}, err
	}
	result := Collection{Sample: service.sample}
	for key, previous := range service.entries {
		if previous.pending == (traffic.Counters{}) {
			continue
		}
		result.Peers = append(result.Peers, Usage{PublicKey: key, Counters: previous.pending})
		if reset {
			service.consume(key, previous)
		}
	}
	return result, nil
}

func (service *Service) check(ctx context.Context, name string) error {
	if name == "" || name != service.name {
		return status.ErrNotFound
	}
	if !service.enabled {
		return status.ErrUnavailable
	}
	return ctx.Err()
}

func (service *Service) consume(key [32]byte, previous entry) {
	if previous.pending == (traffic.Counters{}) {
		return
	}
	if previous.retired {
		delete(service.entries, key)
		return
	}
	previous.pending = traffic.Counters{}
	service.entries[key] = previous
}
