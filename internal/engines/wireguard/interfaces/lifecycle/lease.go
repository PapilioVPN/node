package lifecycle

import (
	"context"
	"time"

	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/status"
)

const leaseGrace = 5 * time.Second

type managementLease struct {
	cancel  context.CancelFunc
	changed chan struct{}
	done    chan struct{}
}

// refreshLease runs with the lifecycle owner held. A disabled lease does not
// allocate a watchdog or a new confirmed status on the heartbeat path.
func (service *Service) refreshLease(interval time.Duration) time.Time {
	var expiry time.Time
	current := service.applied.Load()
	if current != nil && interval != 0 {
		expiry = time.Now().Add(interval + leaseGrace)
		if service.lease == nil {
			ctx, cancel := context.WithCancel(context.Background())
			watch := &managementLease{cancel: cancel, changed: make(chan struct{}, 1), done: make(chan struct{})}
			service.lease = watch
			go service.watchLease(ctx, watch)
		}
	}
	service.leaseInterval = interval
	if current != nil && !current.LeaseExpiresAt.Equal(expiry) {
		next := *current
		next.LeaseExpiresAt = expiry
		service.applied.Store(&next)
	}
	if service.lease != nil {
		select {
		case service.lease.changed <- struct{}{}:
		default:
		}
	}
	return expiry
}

func (service *Service) LeaseExpiresAt(link status.Interface) time.Time {
	current := service.applied.Load()
	if current != nil && current.matches(link) {
		return current.LeaseExpiresAt
	}
	return time.Time{}
}

func (service *Service) watchLease(ctx context.Context, watch *managementLease) {
	defer close(watch.done)
	for {
		var timer *time.Timer
		var elapsed <-chan time.Time
		if current := service.applied.Load(); current != nil && !current.LeaseExpiresAt.IsZero() {
			timer = time.NewTimer(time.Until(current.LeaseExpiresAt))
			elapsed = timer.C
		}
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case <-watch.changed:
			if timer != nil {
				timer.Stop()
			}
		case <-elapsed:
			if !service.expireLease(ctx) {
				timer := time.NewTimer(250 * time.Millisecond)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-watch.changed:
					timer.Stop()
				case <-timer.C:
				}
			}
		}
	}
}

func (service *Service) expireLease(ctx context.Context) bool {
	service.mu.Lock()
	defer service.mu.Unlock()
	current := service.applied.Load()
	if ctx.Err() != nil || current == nil || current.LeaseExpiresAt.IsZero() || time.Now().Before(current.LeaseExpiresAt) {
		return true
	}
	stopCtx, cancel := context.WithTimeout(ctx, OperationTimeout)
	defer cancel()
	if err := service.remove(stopCtx); err != nil {
		return false
	}
	service.emit("keep_alive_expired")
	return true
}
