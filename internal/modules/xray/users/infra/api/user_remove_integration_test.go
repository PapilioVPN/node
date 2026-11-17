//go:build integration

package api_test

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	handlers "github.com/xtls/xray-core/app/proxyman/command"
	stats "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	userdomain "github.com/PapilioVPN/node/internal/modules/xray/users/domain"
	userxray "github.com/PapilioVPN/node/internal/modules/xray/users/infra/api"
	"github.com/PapilioVPN/node/internal/shared/engineapi"
	"github.com/PapilioVPN/node/internal/testutil"
)

// Controlled native service validates the actual wire operation and models loss
// after a removal. Full Xray process/authentication behavior is tested in bootstrap.
type userRemovalServer struct {
	handlers.UnimplementedHandlerServiceServer
	remove func(context.Context, *handlers.AlterInboundRequest) (*handlers.AlterInboundResponse, error)
}

func (s userRemovalServer) AlterInbound(ctx context.Context, r *handlers.AlterInboundRequest) (*handlers.AlterInboundResponse, error) {
	return s.remove(ctx, r)
}

func userAPI(t *testing.T, service handlers.HandlerServiceServer) (string, *testutil.XrayAPICounts) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	counts := &testutil.XrayAPICounts{}
	server := grpc.NewServer(grpc.StatsHandler(counts))
	handlers.RegisterHandlerServiceServer(server, service)
	stats.RegisterStatsServiceServer(server, testutil.XrayRuntimeServer{Read: func(context.Context) (*stats.SysStatsResponse, error) { return &stats.SysStatsResponse{}, nil }})
	done := make(chan struct{})
	go func() { defer close(done); _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); server.GracefulStop(); <-done })
	return listener.Addr().String(), counts
}

func TestUserRemovalUsesOneExactOperationAndSharesStatsChannel(t *testing.T) {
	var calls atomic.Int32
	address, connections := userAPI(t, userRemovalServer{remove: func(_ context.Context, r *handlers.AlterInboundRequest) (*handlers.AlterInboundResponse, error) {
		calls.Add(1)
		operation, err := r.Operation.GetInstance()
		if err != nil {
			return nil, err
		}
		remove, ok := operation.(*handlers.RemoveUserOperation)
		if !ok || r.Tag != " exact>>>tag " || remove.Email != " Mixed@Example " {
			return nil, status.Error(codes.InvalidArgument, "wrong native operation")
		}
		return &handlers.AlterInboundResponse{}, nil
	}})
	client := userClient(t, address, time.Second, 4096)
	if _, err := client.RemoveInboundUser(t.Context(), userdomain.InboundUserSelection{InboundTag: "in", Email: "\xff"}); !errors.Is(err, userdomain.ErrInvalidUserSelection) || errors.Is(err, userdomain.ErrUserMutationUnknown) || calls.Load() != 0 || connections.Opened.Load() != 0 {
		t.Fatalf("failed encoding reached native or claimed effects: %v", err)
	}
	if _, err := client.ReadRuntimeInfo(t.Context()); err != nil {
		t.Fatal(err)
	}
	before := time.Now()
	ack, err := client.RemoveInboundUser(t.Context(), userdomain.InboundUserSelection{InboundTag: " exact>>>tag ", Email: " Mixed@Example "})
	if err != nil || ack.AcknowledgedAt.Before(before) || ack.AcknowledgedAt.After(time.Now()) || calls.Load() != 1 || connections.Opened.Load() != 1 {
		t.Fatalf("removal: %+v %v calls=%d channels=%d", ack, err, calls.Load(), connections.Opened.Load())
	}
	if _, err := client.ReadRuntimeInfo(t.Context()); err != nil || connections.Opened.Load() != 1 {
		t.Fatalf("user operation replaced stats channel: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := client.RemoveInboundUser(t.Context(), userdomain.InboundUserSelection{}); !errors.Is(err, grpc.ErrClientConnClosing) || errors.Is(err, userdomain.ErrUserMutationUnknown) || calls.Load() != 1 {
		t.Fatalf("closed admission: %v calls=%d", err, calls.Load())
	}
}

func TestUserRemovalDoesNotInferNoEffectFromNativeFailureCodes(t *testing.T) {
	for _, code := range []codes.Code{codes.Unimplemented, codes.NotFound, codes.InvalidArgument, codes.Unknown, codes.Unavailable, codes.ResourceExhausted, codes.DeadlineExceeded, codes.Canceled} {
		t.Run(code.String(), func(t *testing.T) {
			var calls atomic.Int32
			address, _ := userAPI(t, userRemovalServer{remove: func(context.Context, *handlers.AlterInboundRequest) (*handlers.AlterInboundResponse, error) {
				calls.Add(1)
				return nil, status.Error(code, "private user/endpoint diagnostics")
			}})
			client := userClient(t, address, time.Second, 4096)
			ack, err := client.RemoveInboundUser(t.Context(), userdomain.InboundUserSelection{InboundTag: "proxy", Email: "user"})
			if err == nil || !ack.AcknowledgedAt.IsZero() || errors.Is(err, userdomain.ErrUserMutationUnknown) != (code != codes.Unimplemented) || status.Code(err) != code || calls.Load() != 1 {
				t.Fatalf("native failure changed/replayed: %+v %v calls=%d", ack, err, calls.Load())
			}
			if code == codes.Unimplemented && !errors.Is(err, userdomain.ErrUserManagementUnsupported) {
				t.Fatal(err)
			}
		})
	}
}

func TestUserRemovalCancellationAfterEffectIsUnknownAndSharedAdmissionIsBounded(t *testing.T) {
	var removed atomic.Bool
	var calls atomic.Int32
	entered := make(chan struct{})
	address, _ := userAPI(t, userRemovalServer{remove: func(ctx context.Context, _ *handlers.AlterInboundRequest) (*handlers.AlterInboundResponse, error) {
		calls.Add(1)
		removed.Store(true)
		close(entered)
		<-ctx.Done()
		return nil, status.FromContextError(ctx.Err()).Err()
	}})
	client := userClient(t, address, 3*time.Second, 4096)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	var removalError error
	go func() {
		defer close(done)
		_, removalError = client.RemoveInboundUser(ctx, userdomain.InboundUserSelection{InboundTag: "proxy", Email: "user"})
	}()
	defer func() { cancel(); _ = client.Close(); <-done }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("native removal did not start")
	}
	if _, err := client.ReadRuntimeInfo(t.Context()); !errors.Is(err, engineapi.ErrBusy) {
		t.Fatalf("stats bypassed shared admission: %v", err)
	}
	if _, err := client.RemoveInboundUser(t.Context(), userdomain.InboundUserSelection{}); !errors.Is(err, engineapi.ErrBusy) || errors.Is(err, userdomain.ErrUserMutationUnknown) {
		t.Fatalf("rejected mutation became uncertain: %v", err)
	}
	cancel()
	<-done
	err := removalError
	if !errors.Is(err, context.Canceled) || !errors.Is(err, userdomain.ErrUserMutationUnknown) || !removed.Load() || calls.Load() != 1 {
		t.Fatalf("applied cancellation lost/replayed effects: %v calls=%d", err, calls.Load())
	}
	if _, err := client.RemoveInboundUser(ctx, userdomain.InboundUserSelection{}); !errors.Is(err, context.Canceled) || errors.Is(err, userdomain.ErrUserMutationUnknown) || calls.Load() != 1 {
		t.Fatalf("pre-canceled admission: %v", err)
	}
}

// Exercise user/stat interoperability through two adapters borrowing one owner.
type userClientSet struct {
	*apiClientSet
	*userxray.Adapter
}

func userClient(t *testing.T, address string, timeout time.Duration, limit int) *userClientSet {
	client := apiClient(t, address, timeout, limit)
	return &userClientSet{apiClientSet: client, Adapter: &userxray.Adapter{API: client.Client}}
}
