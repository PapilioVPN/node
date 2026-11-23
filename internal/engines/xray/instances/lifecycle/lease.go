package lifecycle

import (
	"context"
	"time"
)

const keepAliveGrace = 5 * time.Second

// refreshLeaseLocked runs under mu and control. A single joined watchdog owns
// the timer, and rechecks expiry under control before stopping an execution.
func (service *Service) refreshLeaseLocked(run *execution, interval time.Duration) {
	run.keepAlive = interval
	run.expiresAt = time.Time{}
	if interval > 0 {
		run.expiresAt = time.Now().Add(interval + keepAliveGrace)
		if service.leaseDone == nil {
			service.leaseDone = make(chan struct{})
			go service.watchLease(service.leaseDone)
		}
	}
	service.wakeLease()
}

func (service *Service) wakeLease() {
	select {
	case service.leaseChanged <- struct{}{}:
	default:
	}
}

func (service *Service) watchLease(done chan<- struct{}) {
	defer close(done)
	for {
		service.mu.RLock()
		var expiry time.Time
		if service.state == Ready {
			expiry = service.run.expiresAt
		}
		service.mu.RUnlock()

		var timer *time.Timer
		var elapsed <-chan time.Time
		if !expiry.IsZero() {
			timer = time.NewTimer(time.Until(expiry))
			elapsed = timer.C
		}
		select {
		case <-service.shutdown.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case <-service.leaseChanged:
			if timer != nil {
				timer.Stop()
			}
		case <-elapsed:
			service.expireLease()
		}
	}
}

func (service *Service) expireLease() {
	if err := service.control.Acquire(service.shutdown, 1); err != nil {
		return
	}
	defer service.release()
	service.mu.RLock()
	stale := service.state == Ready && !service.run.expiresAt.IsZero() &&
		!time.Now().Before(service.run.expiresAt)
	service.mu.RUnlock()
	if stale {
		ctx, cancel := context.WithTimeout(service.shutdown, time.Second)
		defer cancel()
		// stop and the process waiter publish the observed state even if the
		// bounded join runs out of time.
		_ = service.stop(ctx, Stopped)
	}
}
