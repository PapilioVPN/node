package status

import (
	"context"
	"time"
)

type PeerStats struct {
	PublicKey     [32]byte
	RXBytes       uint64
	TXBytes       uint64
	LastHandshake time.Time
}

type PeerStatsReader interface {
	ReadPeerStats(context.Context, string) ([]PeerStats, error)
}

func (service *Service) ListPeerStats(ctx context.Context, name string) ([]PeerStats, error) {
	if name == "" || name != service.name {
		return nil, ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return service.peerStats.ReadPeerStats(ctx, name)
}
