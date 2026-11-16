//go:build integration

package api_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	handlers "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	userdomain "github.com/PapilioVPN/node/internal/modules/xray/users/domain"
	"github.com/PapilioVPN/node/internal/shared/engineapi"
)

// Controlled native RPCs prove dispatch/bounds, not real protocol authentication.
type userAdditionServer struct {
	handlers.UnimplementedHandlerServiceServer
	list func(context.Context, *handlers.ListInboundsRequest) (*handlers.ListInboundsResponse, error)
	add  func(context.Context, *handlers.AlterInboundRequest) (*handlers.AlterInboundResponse, error)
}

func (s userAdditionServer) ListInbounds(ctx context.Context, r *handlers.ListInboundsRequest) (*handlers.ListInboundsResponse, error) {
	return s.list(ctx, r)
}
func (s userAdditionServer) AlterInbound(ctx context.Context, r *handlers.AlterInboundRequest) (*handlers.AlterInboundResponse, error) {
	return s.add(ctx, r)
}

func userAddition() userdomain.InboundUserAddition {
	return userdomain.InboundUserAddition{InboundTag: " exact>>>tag ", User: userdomain.InboundUser{Email: " Mixed@Example ", Level: 42,
		Account: userdomain.UserAccount{Protocol: userdomain.VLESS, VLESS: &userdomain.VLESSAccount{ID: "8e781ad6-dae6-4ed4-9d7b-8c247f735869"}}}}
}
func additionTarget() *core.InboundHandlerConfig {
	return &core.InboundHandlerConfig{Tag: userAddition().InboundTag, ProxySettings: &serial.TypedMessage{Type: "xray.proxy.vless.inbound.Config"}}
}

func TestUserAdditionUsesOneListingAndOneAddRegardlessOfOtherInbounds(t *testing.T) {
	for _, count := range []int{1, 100, 1000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			var lists, adds atomic.Int32
			address, connections := userAPI(t, userAdditionServer{
				list: func(_ context.Context, r *handlers.ListInboundsRequest) (*handlers.ListInboundsResponse, error) {
					lists.Add(1)
					if r.IsOnlyTags {
						return nil, status.Error(codes.InvalidArgument, "missing native types")
					}
					items := make([]*core.InboundHandlerConfig, count)
					items[0] = additionTarget()
					for i := 1; i < count; i++ {
						items[i] = &core.InboundHandlerConfig{Tag: fmt.Sprint(i)}
					}
					return &handlers.ListInboundsResponse{Inbounds: items}, nil
				},
				add: func(_ context.Context, r *handlers.AlterInboundRequest) (*handlers.AlterInboundResponse, error) {
					adds.Add(1)
					message, err := r.Operation.GetInstance()
					if err != nil {
						return nil, err
					}
					operation, ok := message.(*handlers.AddUserOperation)
					if !ok || r.Tag != userAddition().InboundTag || operation.User.Email != userAddition().User.Email || operation.User.Level != 42 || operation.User.Account.Type != "xray.proxy.vless.Account" {
						return nil, status.Error(codes.InvalidArgument, "incorrect native operation")
					}
					return &handlers.AlterInboundResponse{}, nil
				},
			})
			client := userClient(t, address, time.Second, 128*1024)
			if _, err := client.ReadRuntimeInfo(t.Context()); err != nil {
				t.Fatal(err)
			}
			before := time.Now()
			ack, err := client.AddInboundUser(t.Context(), userAddition())
			if err != nil || ack.AcknowledgedAt.Before(before) || ack.AcknowledgedAt.After(time.Now()) || lists.Load() != 1 || adds.Load() != 1 || connections.Opened.Load() != 1 {
				t.Fatalf("addition changed dispatch/channel: %v lists=%d adds=%d channels=%d", err, lists.Load(), adds.Load(), connections.Opened.Load())
			}
		})
	}
}

func TestAdditionPreflightFailuresNeverMutateOrClaimUnknownEffect(t *testing.T) {
	for _, tc := range []struct {
		name     string
		inbounds []*core.InboundHandlerConfig
		cause    error
		limit    int
	}{
		{"absent", nil, userdomain.ErrInboundNotFound, 4096},
		{"wrong-family", []*core.InboundHandlerConfig{{Tag: userAddition().InboundTag, ProxySettings: &serial.TypedMessage{Type: "xray.proxy.trojan.ServerConfig"}}}, userdomain.ErrInboundProtocolMismatch, 4096},
		{"duplicate", []*core.InboundHandlerConfig{additionTarget(), additionTarget()}, userdomain.ErrInvalidInboundResponse, 4096},
		{"missing-type", []*core.InboundHandlerConfig{{Tag: userAddition().InboundTag}}, userdomain.ErrInvalidInboundResponse, 4096},
		{"oversized", []*core.InboundHandlerConfig{additionTarget()}, engineapi.ErrResourceLimit, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var adds atomic.Int32
			address, _ := userAPI(t, userAdditionServer{
				list: func(context.Context, *handlers.ListInboundsRequest) (*handlers.ListInboundsResponse, error) {
					return &handlers.ListInboundsResponse{Inbounds: tc.inbounds}, nil
				},
				add: func(context.Context, *handlers.AlterInboundRequest) (*handlers.AlterInboundResponse, error) {
					adds.Add(1)
					return &handlers.AlterInboundResponse{}, nil
				},
			})
			client := userClient(t, address, time.Second, tc.limit)
			ack, err := client.AddInboundUser(t.Context(), userAddition())
			if !errors.Is(err, tc.cause) || errors.Is(err, userdomain.ErrUserMutationUnknown) || !ack.AcknowledgedAt.IsZero() || adds.Load() != 0 {
				t.Fatalf("preflight permitted/claimed a mutation: %v adds=%d", err, adds.Load())
			}
		})
	}
}

func TestAdditionDistinguishesReadFailureFromUncertainMutationWithoutReplay(t *testing.T) {
	for _, code := range []codes.Code{codes.Unimplemented, codes.NotFound, codes.InvalidArgument, codes.Unavailable, codes.ResourceExhausted, codes.Canceled, codes.DeadlineExceeded} {
		for _, failList := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/list=%v", code, failList), func(t *testing.T) {
				var lists, adds atomic.Int32
				address, _ := userAPI(t, userAdditionServer{
					list: func(context.Context, *handlers.ListInboundsRequest) (*handlers.ListInboundsResponse, error) {
						lists.Add(1)
						if failList {
							return nil, status.Error(code, "private diagnostics")
						}
						return &handlers.ListInboundsResponse{Inbounds: []*core.InboundHandlerConfig{additionTarget()}}, nil
					},
					add: func(context.Context, *handlers.AlterInboundRequest) (*handlers.AlterInboundResponse, error) {
						adds.Add(1)
						return nil, status.Error(code, "private diagnostics")
					},
				})
				client := userClient(t, address, time.Second, 4096)
				ack, err := client.AddInboundUser(t.Context(), userAddition())
				var wantAdds int32
				if !failList {
					wantAdds = 1
				}
				if err == nil || !ack.AcknowledgedAt.IsZero() || status.Code(err) != code || errors.Is(err, userdomain.ErrUserMutationUnknown) != (!failList && code != codes.Unimplemented) || lists.Load() != 1 || adds.Load() != wantAdds {
					t.Fatalf("effect/cause/replay changed: %v lists=%d adds=%d", err, lists.Load(), adds.Load())
				}
			})
		}
	}
}

func TestAdditionSharesAdmissionAndOneDeadlineAcrossReadAndWrite(t *testing.T) {
	listed, release := make(chan struct{}), make(chan struct{})
	var firstDeadline time.Time
	var applied atomic.Bool
	address, _ := userAPI(t, userAdditionServer{
		list: func(ctx context.Context, _ *handlers.ListInboundsRequest) (*handlers.ListInboundsResponse, error) {
			firstDeadline, _ = ctx.Deadline()
			close(listed)
			select {
			case <-release:
				return &handlers.ListInboundsResponse{Inbounds: []*core.InboundHandlerConfig{additionTarget()}}, nil
			case <-ctx.Done():
				return nil, status.FromContextError(ctx.Err()).Err()
			}
		},
		add: func(ctx context.Context, _ *handlers.AlterInboundRequest) (*handlers.AlterInboundResponse, error) {
			deadline, _ := ctx.Deadline()
			if deadline.After(firstDeadline.Add(10 * time.Millisecond)) {
				return nil, status.Error(codes.InvalidArgument, "deadline renewed")
			}
			applied.Store(true)
			<-ctx.Done() // Simulate effect followed by loss of its acknowledgement.
			return nil, status.FromContextError(ctx.Err()).Err()
		},
	})
	client := userClient(t, address, 400*time.Millisecond, 4096)
	done := make(chan error, 1)
	go func() { _, err := client.AddInboundUser(t.Context(), userAddition()); done <- err }()
	select {
	case <-listed:
	case <-time.After(3 * time.Second):
		t.Fatal("listing never started")
	}
	if _, err := client.ReadRuntimeInfo(t.Context()); !errors.Is(err, engineapi.ErrBusy) {
		t.Fatalf("stats bypassed shared admission: %v", err)
	}
	close(release)
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, userdomain.ErrUserMutationUnknown) || !applied.Load() {
			t.Fatalf("lost applied acknowledgement became no effect: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("addition failed to observe its deadline")
	}
}
