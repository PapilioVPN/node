package native

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/PapilioVPN/node/internal/engines/xray/stats/traffic"
)

func TestOverlappingTrafficListsShareOnlyANewSample(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var lists trafficLists
		var calls atomic.Int64
		key := trafficListKey{scope: traffic.User}
		release := make(chan struct{})
		load := func(ctx context.Context) ([]traffic.Usage, error) {
			n := calls.Add(1)
			if n == 1 {
				select {
				case <-release:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			return []traffic.Usage{{Name: "alice", UplinkBytes: n, HasUplink: true}}, nil
		}

		type response struct {
			entries []traffic.Usage
			err     error
		}
		start := func(ctx context.Context) <-chan response {
			result := make(chan response, 1)
			go func() {
				entries, err := lists.read(ctx, key, load)
				result <- response{entries, err}
			}()
			return result
		}
		first := start(t.Context())
		synctest.Wait()
		followers := make([]<-chan response, 8)
		for i := range followers {
			followers[i] = start(t.Context())
		}
		ctx, cancel := context.WithCancel(t.Context())
		canceled := start(ctx)
		synctest.Wait()
		cancel()
		if result := <-canceled; !errors.Is(result.err, context.Canceled) {
			t.Fatalf("canceled read = %+v", result)
		}
		close(release)
		if result := <-first; result.err != nil || result.entries[0].UplinkBytes != 1 {
			t.Fatalf("initial read = %+v", result)
		}
		for _, items := range followers {
			result := <-items
			if result.err != nil || result.entries[0].UplinkBytes != 2 {
				t.Fatalf("fresh shared read = %+v", result)
			}
			result.entries[0].UplinkBytes = -1
		}
		if got := calls.Load(); got != 2 {
			t.Fatalf("native reads = %d, want 2", got)
		}
		if result := <-start(t.Context()); result.err != nil || result.entries[0].UplinkBytes != 3 {
			t.Fatalf("independent later read = %+v", result)
		}
	})
}

func TestTrafficListCanRetryAfterAnotherReadersCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var lists trafficLists
		var calls atomic.Int64
		key := trafficListKey{scope: traffic.User}
		release := make(chan struct{})
		load := func(ctx context.Context) ([]traffic.Usage, error) {
			switch calls.Add(1) {
			case 1:
				select {
				case <-release:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			case 2:
				return nil, context.Canceled
			}
			return nil, nil
		}
		read := func() <-chan error {
			finished := make(chan error, 1)
			go func() {
				_, err := lists.read(t.Context(), key, load)
				finished <- err
			}()
			return finished
		}

		first := read()
		synctest.Wait()
		a, b := read(), read()
		synctest.Wait()
		close(release)
		if err := <-first; err != nil {
			t.Fatalf("initial read: %v", err)
		}
		errA, errB := <-a, <-b
		if errA != nil && errB != nil || !errors.Is(errors.Join(errA, errB), context.Canceled) {
			t.Fatalf("expected one canceled read and one successful retry, got %v and %v", errA, errB)
		}
		if got := calls.Load(); got != 3 {
			t.Fatalf("native reads = %d, want 3", got)
		}
	})
}
