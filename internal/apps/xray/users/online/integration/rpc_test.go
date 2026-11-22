//go:build integration && linux

package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/users/online/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/online/infra/api"
)

func TestControlledRPCReceiveBoundReuseAndUnsupportedFacility(t *testing.T) {
	connection, server, peer := newControlledRPC(t)
	reader := api.New(connection)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if r, err := reader.Count(ctx, "a"); err != nil || r.Count == nil || *r.Count != 1 {
		t.Fatal(r, err)
	}
	if r, err := reader.IPs(ctx, "a"); err != nil || len(r.IPs) != 1 || r.IPs[0].LastSeenUnixSeconds != -1 {
		t.Fatal(r, err)
	}
	peer.mu.Lock()
	peers := len(peer.peers)
	peer.mu.Unlock()
	if peer.counts.Load() != 1 || peer.lists.Load() != 1 || peers != 1 {
		t.Fatal("extra native calls or channels", peer.counts.Load(), peer.lists.Load(), peers)
	}
	if r, err := reader.IPs(ctx, "oversized"); !errors.Is(err, domain.ErrCapacity) || r.Tracked || len(r.IPs) != 0 {
		t.Fatal("receive bound returned partial observation", r, err)
	}
	if r, err := reader.Count(ctx, "unsupported"); !errors.Is(err, domain.ErrUnsupported) || r.Count != nil {
		t.Fatal("disabled method became offline", r, err)
	}
	server.Stop()
	if r, err := reader.Count(ctx, "a"); !errors.Is(err, domain.ErrUnavailable) || r.Count != nil {
		t.Fatal("unreachable engine became offline", r, err)
	}
}

func TestControlledRPCDeadlineAndChannelCloseJoinNativeWait(t *testing.T) {
	for _, closeChannel := range []bool{false, true} {
		t.Run(map[bool]string{false: "deadline", true: "close"}[closeChannel], func(t *testing.T) {
			connection, _, peer := newControlledRPC(t)
			reader := api.New(connection)
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				r, err := reader.Count(ctx, "blocked")
				if r.Count != nil {
					err = errors.New("canceled wait returned successful data")
				}
				done <- err
			}()
			select {
			case <-peer.entered:
			case <-ctx.Done():
				t.Fatal("RPC did not enter", ctx.Err())
			}
			if closeChannel {
				if err := connection.Close(); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-done:
				if err == nil || (!closeChannel && !errors.Is(err, context.DeadlineExceeded)) {
					t.Fatal(err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("native caller did not join")
			}
			select {
			case <-peer.exited:
			case <-time.After(2 * time.Second):
				t.Fatal("native peer did not observe cancellation")
			}
		})
	}
}
