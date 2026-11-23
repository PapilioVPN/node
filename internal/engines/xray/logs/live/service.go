// Package live owns subscriptions to the current Xray execution.
package live

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"
)

const (
	MaxMessageBytes  = 16 << 10
	MaxSubscriptions = 16
	QueueCapacity    = 64
)

var (
	ErrNotFound = errors.New("xray instance not found")
	ErrInactive = errors.New("xray instance has no active execution")
	ErrCapacity = errors.New("xray log subscription limit reached")
)

type Source string

const (
	Stdout Source = "stdout"
	Stderr Source = "stderr"
)

type Event struct {
	Execution        uint64
	Sequence         uint64
	Source           Source
	ReceivedUnixNano int64
	Message          string // Immutable bytes, including any invalid UTF-8.
	Truncated        bool
}

type Service struct {
	id string

	mu            sync.Mutex
	current       *Execution
	lastExecution uint64
	subscribers   int
	closed        bool
}

func New(id string) *Service { return &Service{id: id} }

type Execution struct {
	service     *Service
	id          uint64
	ctx         context.Context
	cancel      context.CancelFunc
	sequence    uint64
	subscribers map[*Subscription]struct{}
}

// Prepare binds a publisher to one upcoming child. Only Activate makes it
// available to clients.
func (service *Service) Prepare() *Execution {
	service.mu.Lock()
	defer service.mu.Unlock()
	service.lastExecution++
	ctx, cancel := context.WithCancel(context.Background())
	return &Execution{service: service, id: service.lastExecution, ctx: ctx, cancel: cancel}
}

func (run *Execution) Activate() {
	service := run.service
	service.mu.Lock()
	defer service.mu.Unlock()
	if !service.closed && run.ctx.Err() == nil {
		service.current = run
	}
}

func (run *Execution) End() {
	service := run.service
	service.mu.Lock()
	defer service.mu.Unlock()
	run.endLocked()
}

func (run *Execution) endLocked() {
	if run.service.current == run {
		run.service.current = nil
	}
	clear(run.subscribers)
	run.cancel()
}

func (service *Service) Close() {
	service.mu.Lock()
	defer service.mu.Unlock()
	service.closed = true
	if service.current != nil {
		service.current.endLocked()
	}
}

// Publish copies the bounded line once for all subscribers. A full queue drops
// the newest event for its own subscriber. It never waits for transport I/O.
func (run *Execution) Publish(source Source, line []byte, truncated bool) {
	service := run.service
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.current != run {
		return
	}
	run.sequence++
	if len(run.subscribers) == 0 {
		return
	}
	event := Event{
		Execution: run.id, Sequence: run.sequence, Source: source,
		ReceivedUnixNano: time.Now().UnixNano(), Message: string(line), Truncated: truncated,
	}
	for subscriber := range run.subscribers {
		select {
		case subscriber.events <- event:
		default:
		}
	}
}

type Subscription struct {
	run    *Execution
	events chan Event
	closed bool // Guarded by the service mutex.
}

func (service *Service) Subscribe(id string) (*Subscription, error) {
	if id == "" || id != service.id {
		return nil, ErrNotFound
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	run := service.current
	if run == nil {
		return nil, ErrInactive
	}
	if service.subscribers >= MaxSubscriptions {
		return nil, ErrCapacity
	}
	sub := &Subscription{run: run, events: make(chan Event, QueueCapacity)}
	if run.subscribers == nil {
		run.subscribers = make(map[*Subscription]struct{})
	}
	run.subscribers[sub] = struct{}{}
	service.subscribers++
	return sub, nil
}

func (sub *Subscription) ExecutionContext() context.Context { return sub.run.ctx }

// Next waits for one event, client cancellation or execution end. Ending the
// execution discards any queued events.
func (sub *Subscription) Next(ctx context.Context) (Event, error) {
	select {
	case <-ctx.Done():
		return Event{}, ctx.Err()
	case <-sub.run.ctx.Done():
		return Event{}, io.EOF
	case event := <-sub.events:
		if sub.run.ctx.Err() != nil {
			return Event{}, io.EOF
		}
		if err := ctx.Err(); err != nil {
			return Event{}, err
		}
		return event, nil
	}
}

// Close releases admission after the transport is finished with the queue.
func (sub *Subscription) Close() {
	service := sub.run.service
	service.mu.Lock()
	defer service.mu.Unlock()
	if !sub.closed {
		sub.closed = true
		delete(sub.run.subscribers, sub)
		service.subscribers--
	}
}
