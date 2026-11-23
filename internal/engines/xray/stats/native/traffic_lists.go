package native

import (
	"context"
	"errors"
	"slices"
	"sync"

	"github.com/PapilioVPN/node/internal/engines/xray/stats/traffic"
	"google.golang.org/grpc"
)

type trafficListKey struct {
	conn  *grpc.ClientConn
	scope traffic.Scope
}

type trafficListSample struct {
	done    chan struct{}
	ready   bool
	entries []traffic.Usage
	err     error
}

type trafficListGroup struct {
	readers int
	first   trafficListSample
	latest  *trafficListSample
}

// trafficLists shares the next fresh read for overlapping requests on the
// same execution and scope. It drops the group when the last reader leaves.
type trafficLists struct {
	mu     sync.Mutex
	active map[trafficListKey]*trafficListGroup
}

func (lists *trafficLists) read(ctx context.Context, key trafficListKey, load func(context.Context) ([]traffic.Usage, error)) ([]traffic.Usage, error) {
	lists.mu.Lock()
	group := lists.active[key]
	if group == nil {
		if lists.active == nil {
			lists.active = make(map[trafficListKey]*trafficListGroup)
		}
		group = new(trafficListGroup)
		lists.active[key] = group
	}
	group.readers++
	previous := group.latest
	if previous == nil {
		group.latest = &group.first
	}
	lists.mu.Unlock()
	defer lists.leave(key, group)

	if previous == nil {
		return lists.load(ctx, &group.first, load, false)
	}
	for {
		// The sample already present at admission may predate this request.
		if err := lists.wait(ctx, previous); err != nil {
			return nil, err
		}

		lists.mu.Lock()
		next := group.latest
		if next == previous {
			next = new(trafficListSample)
			group.latest = next
			shared := group.readers > 1
			lists.mu.Unlock()
			return lists.load(ctx, next, load, shared)
		}
		lists.mu.Unlock()

		if err := lists.wait(ctx, next); err != nil {
			return nil, err
		}
		// A different request's cancellation must not cancel this read.
		if errors.Is(next.err, context.Canceled) || errors.Is(next.err, context.DeadlineExceeded) {
			previous = next
			continue
		}
		return slices.Clone(next.entries), next.err
	}
}

func (lists *trafficLists) leave(key trafficListKey, group *trafficListGroup) {
	lists.mu.Lock()
	defer lists.mu.Unlock()
	group.readers--
	if group.readers == 0 {
		delete(lists.active, key)
	}
}

func (lists *trafficLists) load(ctx context.Context, sample *trafficListSample, read func(context.Context) ([]traffic.Usage, error), shared bool) ([]traffic.Usage, error) {
	sample.entries, sample.err = read(ctx)
	lists.mu.Lock()
	sample.ready = true
	if sample.done != nil {
		close(sample.done)
	}
	lists.mu.Unlock()

	if shared {
		return slices.Clone(sample.entries), sample.err
	}
	return sample.entries, sample.err
}

func (lists *trafficLists) wait(ctx context.Context, sample *trafficListSample) error {
	lists.mu.Lock()
	if sample.ready {
		lists.mu.Unlock()
		return ctx.Err()
	}
	if sample.done == nil {
		sample.done = make(chan struct{})
	}
	done := sample.done
	lists.mu.Unlock()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return ctx.Err()
	}
}
