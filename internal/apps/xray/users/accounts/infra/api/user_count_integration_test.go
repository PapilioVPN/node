//go:build integration

package api_test

import (
	"context"
	"errors"
	"math"
	"sync/atomic"
	"testing"
	"time"

	native "github.com/xtls/xray-core/app/proxyman/command"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	userdomain "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/shared/engineapi"
)

type userCountServer struct {
	native.UnimplementedHandlerServiceServer
	count func(context.Context, *native.GetInboundUserRequest) (*native.GetInboundUsersCountResponse, error)
}

func (s userCountServer) GetInboundUsersCount(ctx context.Context, r *native.GetInboundUserRequest) (*native.GetInboundUsersCountResponse, error) {
	return s.count(ctx, r)
}

func TestNativeUserCountHasOneExactCallAndConstantResultSpace(t *testing.T) {
	var calls, size atomic.Int64
	address, connections := userAPI(t, userCountServer{count: func(_ context.Context, r *native.GetInboundUserRequest) (*native.GetInboundUsersCountResponse, error) {
		calls.Add(1)
		if r.Tag != " exact>>>tag " || r.Email != "" {
			return nil, status.Error(codes.InvalidArgument, "unexpected native selection")
		}
		return &native.GetInboundUsersCountResponse{Count: size.Load()}, nil
	}})
	client := userClient(t, address, time.Second, 4096)
	if _, err := client.ReadRuntimeInfo(t.Context()); err != nil {
		t.Fatal(err)
	}
	for i, count := range []int64{0, 1, 100, 1000, math.MaxInt64} {
		size.Store(count)
		started := time.Now()
		result, err := client.ReadInboundUserCount(t.Context(), " exact>>>tag ")
		if err != nil || result.Count != count || result.ObservedAt.Before(started) || result.ObservedAt.After(time.Now()) || calls.Load() != int64(i+1) || connections.Opened.Load() != 1 {
			t.Fatalf("count precision/dispatch/channel changed: %+v %v calls=%d", result, err, calls.Load())
		}
	}
	size.Store(-1)
	if result, err := client.ReadInboundUserCount(t.Context(), " exact>>>tag "); !errors.Is(err, userdomain.ErrInvalidUserCount) || result != (userdomain.InboundUserCount{}) {
		t.Fatalf("negative native count became successful facts: %+v %v", result, err)
	}
}

func TestNativeUserCountPreservesFailureInsteadOfReturningZero(t *testing.T) {
	for _, code := range []codes.Code{codes.NotFound, codes.Unimplemented, codes.Unavailable, codes.ResourceExhausted, codes.Canceled, codes.DeadlineExceeded} {
		t.Run(code.String(), func(t *testing.T) {
			var calls atomic.Int32
			address, _ := userAPI(t, userCountServer{count: func(context.Context, *native.GetInboundUserRequest) (*native.GetInboundUsersCountResponse, error) {
				calls.Add(1)
				return nil, status.Error(code, "private native diagnostics")
			}})
			client := userClient(t, address, time.Second, 4096)
			result, err := client.ReadInboundUserCount(t.Context(), "in")
			if status.Code(err) != code || result != (userdomain.InboundUserCount{}) || calls.Load() != 1 {
				t.Fatalf("failed count became zero/retried: %+v %v calls=%d", result, err, calls.Load())
			}
		})
	}
	address, _ := userAPI(t, userCountServer{count: func(context.Context, *native.GetInboundUserRequest) (*native.GetInboundUsersCountResponse, error) {
		return &native.GetInboundUsersCountResponse{Count: math.MaxInt64}, nil
	}})
	client := userClient(t, address, time.Second, 1)
	if result, err := client.ReadInboundUserCount(t.Context(), "in"); !errors.Is(err, engineapi.ErrResourceLimit) || result != (userdomain.InboundUserCount{}) {
		t.Fatalf("native receive limit was bypassed: %+v %v", result, err)
	}
}

func TestNativeUserCountSharesAdmissionAndCancellationWithOtherOperations(t *testing.T) {
	entered := make(chan struct{})
	address, _ := userAPI(t, userCountServer{count: func(ctx context.Context, _ *native.GetInboundUserRequest) (*native.GetInboundUsersCountResponse, error) {
		close(entered)
		<-ctx.Done()
		return nil, status.FromContextError(ctx.Err()).Err()
	}})
	client := userClient(t, address, time.Second, 4096)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { _, err := client.ReadInboundUserCount(ctx, "in"); done <- err }()
	defer func() { cancel(); _ = client.Close() }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("count never reached native API")
	}
	if _, err := client.ReadRuntimeInfo(t.Context()); !errors.Is(err, engineapi.ErrBusy) {
		t.Fatalf("stats bypassed count admission: %v", err)
	}
	if _, err := client.RemoveInboundUser(t.Context(), userdomain.InboundUserSelection{InboundTag: "in", Email: "user"}); !errors.Is(err, engineapi.ErrBusy) {
		t.Fatalf("mutation bypassed count admission: %v", err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("count lost cancellation: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("count failed to join after cancellation")
	}
	if result, err := client.ReadInboundUserCount(ctx, "in"); !errors.Is(err, context.Canceled) || result != (userdomain.InboundUserCount{}) {
		t.Fatal("pre-canceled count returned successful facts")
	}
}
