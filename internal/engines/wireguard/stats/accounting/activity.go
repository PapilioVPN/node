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
	value, ok := service.activity.Load([32]byte(key))
	if !ok {
		return Activity{}, nil
	}
	observed := value.(Activity)
	observed.Active = observed.LastActive.After(time.Now().Add(-activeWindow))
	return observed, nil
}

func (service *Service) publishActivity(row traffic.PeerStats, at time.Time, advanced bool) {
	value, _ := service.activity.Load(row.PublicKey)
	current, _ := value.(Activity)
	next := current
	if advanced {
		next.LastActive = at
	}
	if !next.LastActive.IsZero() && (advanced || current.Endpoint != row.EndpointIP) {
		next.Endpoint = row.EndpointIP
		service.activity.Store(row.PublicKey, next)
	}
}
