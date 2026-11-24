// Package traffic reads live WireGuard link and peer counters.
package traffic

import (
	"context"
	"time"

	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/status"
)

type Counters struct {
	RXBytes uint64
	TXBytes uint64
}

type PeerStats struct {
	PublicKey [32]byte
	Counters
	LastHandshake time.Time
}

type LinkReader interface {
	ReadInterfaceTraffic(context.Context, string) (Counters, error)
}

type PeerReader interface {
	ReadPeerStats(context.Context, string) ([]PeerStats, error)
}

type Service struct {
	name  string
	links LinkReader
	peers PeerReader
}

func New(name string, links LinkReader, peers PeerReader) *Service {
	return &Service{name: name, links: links, peers: peers}
}

func (service *Service) GetInterfaceTraffic(ctx context.Context, name string) (Counters, error) {
	if name == "" || name != service.name {
		return Counters{}, status.ErrNotFound
	}
	return service.links.ReadInterfaceTraffic(ctx, name)
}

func (service *Service) ListPeerStats(ctx context.Context, name string) ([]PeerStats, error) {
	if name == "" || name != service.name {
		return nil, status.ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return service.peers.ReadPeerStats(ctx, name)
}
