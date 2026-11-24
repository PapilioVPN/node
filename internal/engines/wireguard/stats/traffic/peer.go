package traffic

import (
	"context"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/peers"
	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/status"
)

var (
	ErrInvalidPeerKey = errors.New("invalid wireguard peer public key")
	ErrPeerNotFound   = errors.New("wireguard peer not found")
	ErrResetDisabled  = errors.New("wireguard peer traffic reset requires managed mode")
	ErrResetCapacity  = errors.New("wireguard peer traffic reset capacity reached")
)

type PeerTrafficSample struct {
	Counters
	LinkIndex uint32
}

type peerReset struct {
	base Counters
	last PeerTrafficSample
}

func (service *Service) GetPeerTraffic(ctx context.Context, name string, key []byte) (Counters, error) {
	return service.readPeerTraffic(ctx, name, key, false)
}

func (service *Service) ResetPeerTraffic(ctx context.Context, name string, key []byte) (Counters, error) {
	return service.readPeerTraffic(ctx, name, key, true)
}

func (service *Service) readPeerTraffic(ctx context.Context, name string, key []byte, reset bool) (Counters, error) {
	if len(key) != 32 || [32]byte(key) == [32]byte{} {
		return Counters{}, ErrInvalidPeerKey
	}
	if name == "" || name != service.name {
		return Counters{}, status.ErrNotFound
	}
	if reset && !service.managed {
		return Counters{}, ErrResetDisabled
	}
	if !service.peerRead.TryLock() {
		return Counters{}, status.ErrBusy
	}
	defer service.peerRead.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	// A removal can finish while the socket is reading. Reject a sample that
	// crossed that boundary before it can restore an obsolete reset baseline.
	publicKey := [32]byte(key)
	service.mu.Lock()
	revision := service.revision
	service.mu.Unlock()
	sample, err := service.peers.ReadPeerTraffic(ctx, name, publicKey)
	if canceled := ctx.Err(); canceled != nil {
		return Counters{}, canceled
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if revision != service.revision {
		return Counters{}, status.ErrBusy
	}
	if err != nil {
		if errors.Is(err, ErrPeerNotFound) {
			delete(service.resets, publicKey)
		}
		return Counters{}, err
	}
	previous, exists := service.resets[publicKey]
	if exists && (sample.LinkIndex != previous.last.LinkIndex || sample.RXBytes < previous.last.RXBytes || sample.TXBytes < previous.last.TXBytes) {
		delete(service.resets, publicKey)
		previous, exists = peerReset{}, false
	}
	result := Counters{RXBytes: sample.RXBytes - previous.base.RXBytes, TXBytes: sample.TXBytes - previous.base.TXBytes}
	if reset {
		if !exists && len(service.resets) >= peers.MaxPeers {
			return Counters{}, ErrResetCapacity
		}
		if service.resets == nil {
			service.resets = make(map[[32]byte]peerReset)
		}
		service.resets[publicKey] = peerReset{base: sample.Counters, last: sample}
	} else if exists {
		previous.last = sample
		service.resets[publicKey] = previous
	}
	return result, nil
}

func (service *Service) ForgetPeerTraffic(key [32]byte) {
	service.mu.Lock()
	defer service.mu.Unlock()
	service.revision++
	delete(service.resets, key)
}

func (service *Service) ForgetLinkTraffic() {
	service.mu.Lock()
	defer service.mu.Unlock()
	service.revision++
	clear(service.resets)
	service.linkRevision++
	service.linkBase = LinkTrafficSample{}
}
