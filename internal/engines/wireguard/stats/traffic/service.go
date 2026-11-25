// Package traffic reads live WireGuard link and peer counters.
package traffic

import (
	"context"
	"net/netip"
	"sync"
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
	EndpointIP    netip.Addr
}

type LinkReader interface {
	ReadInterfaceTraffic(context.Context, string) (LinkTrafficSample, error)
}

type PeerReader interface {
	ReadPeerStats(context.Context, string) ([]PeerStats, error)
	ReadPeerTraffic(context.Context, string, [32]byte) (PeerTrafficSample, error)
}

type Service struct {
	name    string
	links   LinkReader
	peers   PeerReader
	managed bool

	peerRead     sync.Mutex
	linkRead     sync.Mutex
	mu           sync.Mutex
	revision     uint64
	resets       map[[32]byte]peerReset
	linkRevision uint64
	linkBase     LinkTrafficSample
}

func New(name string, links LinkReader, peers PeerReader, managed bool) *Service {
	return &Service{name: name, links: links, peers: peers, managed: managed}
}

func (service *Service) GetInterfaceTraffic(ctx context.Context, name string) (Counters, error) {
	if name == "" || name != service.name {
		return Counters{}, status.ErrNotFound
	}
	sample, err := service.links.ReadInterfaceTraffic(ctx, name)
	return sample.Counters, err
}

func (service *Service) ListPeerStats(ctx context.Context, name string) ([]PeerStats, error) {
	if name == "" || name != service.name {
		return nil, status.ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return service.peers.ReadPeerStats(ctx, name)
}
