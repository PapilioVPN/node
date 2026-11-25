package accounting

import (
	"context"
	"net/netip"
	"time"

	"github.com/PapilioVPN/node/internal/engines/wireguard/stats/traffic"
)

type Activity struct {
	Active     bool
	LastActive time.Time
	Endpoint   netip.Addr
}

func (service *Service) PeerActivity(ctx context.Context, name string, key []byte) (Activity, error) {
	if len(key) != 32 || [32]byte(key) == [32]byte{} {
		return Activity{}, traffic.ErrInvalidPeerKey
	}
	if err := service.check(ctx, name); err != nil {
		return Activity{}, err
	}
	service.mu.RLock()
	peer := service.entries[[32]byte(key)]
	service.mu.RUnlock()
	if peer.lastActive.IsZero() {
		return Activity{}, nil
	}
	return Activity{
		Active:     peer.lastActive.After(time.Now().Add(-activeWindow)),
		LastActive: peer.lastActive,
		Endpoint:   peer.endpoint,
	}, nil
}
