// Package logs owns bounded live subscriptions for one Hysteria execution.
package live

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"
)

const maxSubscriptions = 16
const queueCapacity = 64

var ErrCapacity = errors.New("Hysteria log subscription limit reached")
var ErrInactive = errors.New("Hysteria execution ended")

type Event struct {
	Execution        uint64 `json:"execution,string"`
	Sequence         uint64 `json:"sequence,string"`
	ReceivedUnixNano int64  `json:"received_unix_nano,string"`
	Name             string `json:"event"`
}

type Service struct {
	current func(context.Context, string) (*Journal, error)
	slots   chan struct{}
	ctx     context.Context
	cancel  context.CancelFunc
}

func New(current func(context.Context, string) (*Journal, error)) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{current: current, slots: make(chan struct{}, maxSubscriptions), ctx: ctx, cancel: cancel}
}

// Close stops admission and cancels streams before management listener drain.
func (s *Service) Close() { s.cancel() }

func (s *Service) Subscribe(ctx context.Context, id string) (*Subscription, error) {
	j, err := s.current(ctx, id)
	if err != nil {
		return nil, err
	}
	select {
	case s.slots <- struct{}{}:
	default:
		return nil, ErrCapacity
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.ctx.Err() != nil || s.ctx.Err() != nil {
		<-s.slots
		return nil, ErrInactive
	}
	lifetime, cancel := context.WithCancel(j.ctx)
	stop := context.AfterFunc(s.ctx, cancel)
	sub := &Subscription{journal: j, events: make(chan Event, queueCapacity), slots: s.slots, lifetime: lifetime,
		cancel: func() { stop(); cancel() },
	}
	j.subs[sub] = struct{}{}
	return sub, nil
}

type Journal struct {
	ctx      context.Context
	number   uint64
	mu       sync.Mutex
	sequence uint64
	subs     map[*Subscription]struct{}
}

func NewJournal(ctx context.Context, number uint64) *Journal {
	return &Journal{ctx: ctx, number: number, subs: make(map[*Subscription]struct{})}
}

// Publish accepts only canonical events produced by the process adapter. Drop
// new events for a full subscriber queue, leaving observable sequence gaps.
func (j *Journal) Publish(name string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.ctx.Err() != nil {
		return
	}
	j.sequence++
	if len(j.subs) == 0 {
		return
	}
	event := Event{Execution: j.number, Sequence: j.sequence, ReceivedUnixNano: time.Now().UnixNano(), Name: name}
	for sub := range j.subs {
		select {
		case sub.events <- event:
		default:
		}
	}
}

type Subscription struct {
	journal  *Journal
	events   chan Event
	slots    chan struct{}
	once     sync.Once
	lifetime context.Context
	cancel   func()
}

func (s *Subscription) Context() context.Context { return s.lifetime }
func (s *Subscription) Close() {
	s.once.Do(func() {
		s.cancel()
		j := s.journal
		j.mu.Lock()
		delete(j.subs, s)
		j.mu.Unlock()
		<-s.slots
	})
}

func (s *Subscription) Next(ctx context.Context) (Event, error) {
	select {
	case <-ctx.Done():
		return Event{}, ctx.Err()
	case <-s.Context().Done():
		return Event{}, io.EOF
	case event := <-s.events:
		if s.Context().Err() != nil {
			return Event{}, io.EOF
		}
		if ctx.Err() != nil {
			return Event{}, ctx.Err()
		}
		return event, nil
	}
}
