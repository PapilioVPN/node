//go:build linux

package process

import (
	"context"
	"sync"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
)

// Admission owns bounded converter work across instances, including cancellation
// and joining during Node shutdown. Saturation never queues a check.
type Admission struct {
	mu       sync.Mutex
	slots    chan struct{}
	active   int
	closed   bool
	lifetime context.Context
	cancel   context.CancelFunc
	done     chan struct{}
}

func NewAdmission(capacity int) (*Admission, error) {
	if capacity <= 0 {
		return nil, domain.ErrInvalidArgument
	}
	lifetime, cancel := context.WithCancel(context.Background())
	return &Admission{slots: make(chan struct{}, capacity), lifetime: lifetime, cancel: cancel, done: make(chan struct{})}, nil
}

func (a *Admission) acquire(parent context.Context) (context.Context, func(), error) {
	if err := parent.Err(); err != nil {
		return nil, nil, err
	}
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil, nil, domain.ErrClosed
	}
	select {
	case a.slots <- struct{}{}:
		a.active++
	default:
		a.mu.Unlock()
		return nil, nil, domain.ErrCapacity
	}
	a.mu.Unlock()
	ctx, cancel := context.WithCancel(parent)
	notified := make(chan struct{})
	stop := context.AfterFunc(a.lifetime, func() { cancel(); close(notified) })
	return ctx, func() {
		if !stop() {
			<-notified
		}
		cancel()
		a.mu.Lock()
		defer a.mu.Unlock()
		<-a.slots
		a.active--
		if a.closed && a.active == 0 {
			close(a.done)
		}
	}, nil
}

func (a *Admission) Close(ctx context.Context) error {
	a.mu.Lock()
	if !a.closed {
		a.closed = true
		a.cancel()
		if a.active == 0 {
			close(a.done)
		}
	}
	a.mu.Unlock()
	select {
	case <-a.done:
		return nil
	default:
	}
	select {
	case <-a.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
