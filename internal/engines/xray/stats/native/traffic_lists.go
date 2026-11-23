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
	entries []traffic.Usage
	err     error
}

type trafficListGroup struct {
	readers int
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
	lists.mu.Unlock()
	defer lists.leave(key, group)

	for {
		// The sample already present at admission may predate this request.
		if previous != nil {
			if err := previous.wait(ctx); err != nil {
				return nil, err
			}
		}

		lists.mu.Lock()
		next := group.latest
		if next == previous {
			next = &trafficListSample{done: make(chan struct{})}
			group.latest = next
			shared := group.readers > 1
			lists.mu.Unlock()

			next.entries, next.err = load(ctx)
			close(next.done)
			if shared {
				return slices.Clone(next.entries), next.err
			}
			return next.entries, next.err
		}
		lists.mu.Unlock()

		if err := next.wait(ctx); err != nil {
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

func (sample *trafficListSample) wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-sample.done:
		return ctx.Err()
	}
}
