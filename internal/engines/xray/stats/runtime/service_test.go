package runtime

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"google.golang.org/grpc"
)

func TestConcurrentReadsShareOnlyTheNextSample(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		var calls atomic.Uint32
		release := make(chan struct{})
		service := newService(fixedInstance{conn: new(grpc.ClientConn)}, func(ctx context.Context, _ *grpc.ClientConn) (Snapshot, error) {
			count := calls.Add(1)
			if count == 1 {
				select {
				case <-release:
				case <-ctx.Done():
					return Snapshot{}, ctx.Err()
				}
			}
			return Snapshot{UptimeSeconds: count}, nil
		})
		defer func() { _ = service.Close(ctx) }()

		first := startRead(service, ctx)
		synctest.Wait()
		if calls.Load() != 1 {
			t.Fatal("the first read did not enter Xray")
		}

		queued := make([]<-chan readResult, 64)
		for i := range queued {
			queued[i] = startRead(service, ctx)
		}
		canceled, cancel := context.WithCancel(ctx)
		canceledRead := startRead(service, canceled)
		synctest.Wait()
		cancel()
		if got := <-canceledRead; !errors.Is(got.err, context.Canceled) {
			t.Fatalf("canceled read = %+v", got)
		}
		close(release)

		expectSample(t, first, 1)
		for _, result := range queued {
			expectSample(t, result, 2)
		}
		synctest.Wait()
		if got := calls.Load(); got != 2 {
			t.Fatalf("native reads = %d, want 2", got)
		}
		expectSample(t, startRead(service, ctx), 3)
		if err := service.Close(ctx); err != nil {
			t.Fatal(err)
		}
	})
}

func TestRestartFencesQueuedSamples(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		source := new(changingInstance)
		original := new(grpc.ClientConn)
		source.conn.Store(original)
		release := make(chan struct{})
		service := newService(source, func(ctx context.Context, conn *grpc.ClientConn) (Snapshot, error) {
			if conn == original {
				select {
				case <-release:
				case <-ctx.Done():
					return Snapshot{}, ctx.Err()
				}
				return Snapshot{UptimeSeconds: 100}, nil
			}
			return Snapshot{UptimeSeconds: 1}, nil
		})
		defer func() { _ = service.Close(ctx) }()

		first := startRead(service, ctx)
		synctest.Wait()
		stale := startRead(service, ctx)
		synctest.Wait()
		source.conn.Store(new(grpc.ClientConn))
		replacement := startRead(service, ctx)
		synctest.Wait()

		close(release)
		if got := <-stale; !errors.Is(got.err, ErrUnavailable) {
			t.Fatalf("queued read for replaced instance = %+v", got)
		}
		if got := <-first; !errors.Is(got.err, ErrUnavailable) {
			t.Fatalf("native read for replaced instance = %+v", got)
		}
		expectSample(t, replacement, 1)
		if err := service.Close(ctx); err != nil {
			t.Fatal(err)
		}
	})
}

func TestRuntimeStatsRejectsReadsAtCapacity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		service := newService(fixedInstance{conn: new(grpc.ClientConn)}, func(ctx context.Context, _ *grpc.ClientConn) (Snapshot, error) {
			<-ctx.Done()
			return Snapshot{}, ctx.Err()
		})
		defer func() { _ = service.Close(ctx) }()
		_ = startRead(service, ctx)
		synctest.Wait()
		for range queueSize + 1 {
			_ = startRead(service, ctx)
		}
		synctest.Wait()
		_, err := service.GetRuntimeStats(ctx, "primary")
		if !errors.Is(err, ErrBusy) {
			t.Fatalf("GetRuntimeStats = %v, want ErrBusy", err)
		}
	})
}

func TestCloseCancelsAndJoinsSharedRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		release := make(chan struct{})
		var calls atomic.Uint32
		service := newService(fixedInstance{conn: new(grpc.ClientConn)}, func(ctx context.Context, _ *grpc.ClientConn) (Snapshot, error) {
			if calls.Add(1) == 1 {
				select {
				case <-release:
				case <-ctx.Done():
					return Snapshot{}, ctx.Err()
				}
				return Snapshot{UptimeSeconds: 1}, nil
			}
			<-ctx.Done()
			return Snapshot{}, ctx.Err()
		})
		defer func() { _ = service.Close(ctx) }()

		first := startRead(service, ctx)
		synctest.Wait()
		queued := startRead(service, ctx)
		synctest.Wait()
		close(release)
		expectSample(t, first, 1)
		synctest.Wait()
		if calls.Load() != 2 {
			t.Fatal("the shared read did not start")
		}
		if err := service.Close(ctx); err != nil {
			t.Fatal(err)
		}
		if result := <-queued; !errors.Is(result.err, context.Canceled) && !errors.Is(result.err, ErrUnavailable) {
			t.Fatalf("shared read after close = %+v", result)
		}
	})
}

func startRead(service *Service, ctx context.Context) <-chan readResult {
	result := make(chan readResult, 1)
	go func() {
		snapshot, err := service.GetRuntimeStats(ctx, "primary")
		result <- readResult{snapshot, err}
	}()
	return result
}

func expectSample(t *testing.T, result <-chan readResult, sequence uint32) {
	t.Helper()
	value := <-result
	if value.err != nil || value.snapshot.UptimeSeconds != sequence {
		t.Fatalf("runtime stats = %+v, want sample %d", value, sequence)
	}
}

type fixedInstance struct{ conn *grpc.ClientConn }

func (instance fixedInstance) NativeConnection(string) (*grpc.ClientConn, error) {
	return instance.conn, nil
}

type changingInstance struct {
	conn atomic.Pointer[grpc.ClientConn]
}

func (instance *changingInstance) NativeConnection(string) (*grpc.ClientConn, error) {
	return instance.conn.Load(), nil
}
