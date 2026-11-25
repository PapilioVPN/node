// Package events owns bounded WireGuard management log subscriptions.
package events

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/status"
)

const historyLimit = 64
const subscriberLimit = 16

var ErrCapacity = errors.New("wireguard log subscriber limit reached")

type Event struct {
	Sequence uint64    `json:"sequence,string"`
	At       time.Time `json:"at"`
	Kind     string    `json:"kind"`
}

type Service struct {
	name        string
	mu          sync.Mutex
	seq         uint64
	closed      bool
	history     []Event
	subscribers map[*Subscription]struct{}
}

type Subscription struct {
	owner   *Service
	Context context.Context
	cancel  context.CancelFunc
	queue   chan Event
}

func New(name string) *Service {
	return &Service{name: name, subscribers: make(map[*Subscription]struct{})}
}

func (service *Service) Publish(kind string) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.closed {
		return
	}
	service.seq++
	event := Event{Sequence: service.seq, At: time.Now().UTC(), Kind: kind}
	if len(service.history) == historyLimit {
		copy(service.history, service.history[1:])
		service.history[len(service.history)-1] = event
	} else {
		service.history = append(service.history, event)
	}
	for sub := range service.subscribers {
		select {
		case sub.queue <- event:
		default:
			delete(service.subscribers, sub)
			sub.cancel()
		}
	}
}

func (service *Service) Subscribe(ctx context.Context, name string) (*Subscription, error) {
	if name == "" || name != service.name {
		return nil, status.ErrNotFound
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.closed {
		return nil, status.ErrUnavailable
	}
	if len(service.subscribers) == subscriberLimit {
		return nil, ErrCapacity
	}
	ctx, cancel := context.WithCancel(ctx)
	sub := &Subscription{owner: service, Context: ctx, cancel: cancel, queue: make(chan Event, 2*historyLimit)}
	for _, event := range service.history {
		sub.queue <- event
	}
	service.subscribers[sub] = struct{}{}
	return sub, nil
}

func (sub *Subscription) Next() (Event, error) {
	select {
	case <-sub.Context.Done():
		return Event{}, sub.Context.Err()
	case event := <-sub.queue:
		if err := sub.Context.Err(); err != nil {
			return Event{}, err
		}
		return event, nil
	}
}

func (sub *Subscription) Close() {
	service := sub.owner
	service.mu.Lock()
	defer service.mu.Unlock()
	delete(service.subscribers, sub)
	sub.cancel()
}

func (service *Service) Close(context.Context) error {
	service.mu.Lock()
	defer service.mu.Unlock()
	service.closed = true
	for sub := range service.subscribers {
		sub.cancel()
	}
	clear(service.subscribers)
	return nil
}
