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
	"github.com/xtls/xray-core/core"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/shared/engineapi"
)

func userAdditions(count int) domain.InboundUserAdditions {
	input := domain.InboundUserAdditions{InboundTag: userAddition().InboundTag, Users: make([]domain.InboundUser, count)}
	for i := range input.Users {
		input.Users[i] = userAddition().User
		input.Users[i].Email = fmt.Sprintf(" opaque>>>%d ", i)
	}
	return input
}

// Controlled SDK servers verify dispatch and partial effects, not authentication.
func TestUserAdditionsInspectOneInboundOnceForTheWholeCollection(t *testing.T) {
	for _, count := range []int{0, 1, 100, 1000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			var lists, adds atomic.Int32
			input := userAdditions(count)
			address, connections := userAPI(t, userAdditionServer{
				list: func(context.Context, *handlers.ListInboundsRequest) (*handlers.ListInboundsResponse, error) {
					lists.Add(1)
					return &handlers.ListInboundsResponse{Inbounds: []*core.InboundHandlerConfig{additionTarget()}}, nil
				},
				add: func(_ context.Context, request *handlers.AlterInboundRequest) (*handlers.AlterInboundResponse, error) {
					i := int(adds.Add(1)) - 1
					message, err := request.Operation.GetInstance()
					if err != nil {
						t.Error(err)
						return nil, status.Error(codes.InvalidArgument, "invalid native encoding")
					}
					operation, ok := message.(*handlers.AddUserOperation)
					if !ok || request.Tag != input.InboundTag || operation.User.Email != input.Users[i].Email || operation.User.Level != input.Users[i].Level {
						t.Error("collection changed exact native input/order")
					}
					return &handlers.AlterInboundResponse{}, nil
				},
			})
			client := userClient(t, address, 10*time.Second, 4096)
			before := time.Now()
			report, err := client.AddInboundUsers(t.Context(), input)
			wantLists := int32(0)
			if count > 0 {
				wantLists = 1
			}
			if err != nil || len(report.Items) != count || adds.Load() != int32(count) || lists.Load() != wantLists || connections.Opened.Load() != wantLists {
				t.Fatalf("collection added preflights/channels or lost results: err=%v items=%d lists=%d adds=%d channels=%d", err, len(report.Items), lists.Load(), adds.Load(), connections.Opened.Load())
			}
			for i, item := range report.Items {
				if item.Email != input.Users[i].Email || item.State != domain.UserMutationAcknowledged || item.AcknowledgedAt.Before(before) || item.AcknowledgedAt.After(time.Now()) || item.Failure != nil {
					t.Fatal("native acknowledgements lost identity, order or receipt time")
				}
			}
		})
	}
}

func TestUserAdditionsStopWithoutReplayingOrHidingEarlierEffects(t *testing.T) {
	for _, code := range []codes.Code{codes.Unimplemented, codes.Unavailable, codes.InvalidArgument, codes.NotFound, codes.ResourceExhausted, codes.Canceled, codes.DeadlineExceeded} {
		t.Run(code.String(), func(t *testing.T) {
			var adds atomic.Int32
			address, _ := userAPI(t, userAdditionServer{
				list: func(context.Context, *handlers.ListInboundsRequest) (*handlers.ListInboundsResponse, error) {
					return &handlers.ListInboundsResponse{Inbounds: []*core.InboundHandlerConfig{additionTarget()}}, nil
				},
				add: func(context.Context, *handlers.AlterInboundRequest) (*handlers.AlterInboundResponse, error) {
					if adds.Add(1) == 2 {
						return nil, status.Error(code, "private credential diagnostics")
					}
					return &handlers.AlterInboundResponse{}, nil
				},
			})
			report, err := userClient(t, address, time.Second, 4096).AddInboundUsers(t.Context(), userAdditions(4))
			unknown := code != codes.Unimplemented
			if status.Code(err) != code || errors.Is(err, domain.ErrUserMutationUnknown) != unknown || adds.Load() != 2 || len(report.Items) != 4 {
				t.Fatalf("failure lost cause, effects or stopped admission: %v calls=%d items=%d", err, adds.Load(), len(report.Items))
			}
			wantFailure := domain.UserMutationNotApplied
			if unknown {
				wantFailure = domain.UserMutationUnknown
			}
			for i, state := range []domain.UserMutationState{domain.UserMutationAcknowledged, wantFailure, domain.UserMutationNotAttempted, domain.UserMutationNotAttempted} {
				item := report.Items[i]
				if item.State != state || (item.Failure != nil) != (i == 1) || item.AcknowledgedAt.IsZero() != (i != 0) {
					t.Fatalf("partial outcome at index %d changed", i)
				}
			}
		})
	}
}

func TestUserAdditionsRejectTheWholePreparationBeforeAnyMutation(t *testing.T) {
	for _, scenario := range []string{"uuid", "utf8", "mixed", "missing", "wrong-type", "duplicate", "missing-type", "unsupported", "receive-limit"} {
		t.Run(scenario, func(t *testing.T) {
			var lists, adds atomic.Int32
			input := userAdditions(3)
			inbounds := []*core.InboundHandlerConfig{additionTarget()}
			cause, limit := domain.ErrInvalidUserAccount, 4096
			wantLists := int32(0)
			switch scenario {
			case "uuid":
				input.Users[2].Account.VLESS.ID = "invalid-uuid-with-more-than-thirty-characters"
			case "utf8":
				input.Users[2].Email = "\xff"
			case "mixed":
				input.Users[2].Account = domain.UserAccount{Protocol: domain.Trojan, Trojan: &domain.PasswordAccount{Password: "test-only"}}
				cause = domain.ErrInboundProtocolMismatch
			case "missing":
				inbounds, cause, wantLists = nil, domain.ErrInboundNotFound, 1
			case "wrong-type":
				inbounds[0].ProxySettings.Type = "xray.proxy.trojan.ServerConfig"
				cause, wantLists = domain.ErrInboundProtocolMismatch, 1
			case "duplicate":
				inbounds = append(inbounds, additionTarget())
				cause, wantLists = domain.ErrInvalidInboundResponse, 1
			case "missing-type":
				inbounds[0].ProxySettings = nil
				cause, wantLists = domain.ErrInvalidInboundResponse, 1
			case "unsupported":
				cause, wantLists = domain.ErrUserManagementUnsupported, 1
			case "receive-limit":
				cause, limit, wantLists = engineapi.ErrResourceLimit, 1, 1
			}
			address, connections := userAPI(t, userAdditionServer{
				list: func(context.Context, *handlers.ListInboundsRequest) (*handlers.ListInboundsResponse, error) {
					lists.Add(1)
					if scenario == "unsupported" {
						return nil, status.Error(codes.Unimplemented, "private native detail")
					}
					return &handlers.ListInboundsResponse{Inbounds: inbounds}, nil
				},
				add: func(context.Context, *handlers.AlterInboundRequest) (*handlers.AlterInboundResponse, error) {
					adds.Add(1)
					return &handlers.AlterInboundResponse{}, nil
				},
			})
			report, err := userClient(t, address, time.Second, limit).AddInboundUsers(t.Context(), input)
			if !errors.Is(err, cause) || errors.Is(err, domain.ErrUserMutationUnknown) || adds.Load() != 0 || lists.Load() != wantLists || connections.Opened.Load() != wantLists || len(report.Items) != 3 {
				t.Fatalf("preparation created effects or lost their absence: err=%v lists=%d adds=%d", err, lists.Load(), adds.Load())
			}
			for i, item := range report.Items {
				if item.Email != input.Users[i].Email || item.State != domain.UserMutationNotAttempted || !item.AcknowledgedAt.IsZero() || item.Failure != nil {
					t.Fatal("rejected preparation reported individual effects")
				}
			}
		})
	}
}

func TestUserAdditionsKeepOneDeadlineAndJoinInterruptedPartialEffects(t *testing.T) {
	for _, mode := range []string{"cancel", "timeout", "close"} {
		t.Run(mode, func(t *testing.T) {
			var adds atomic.Int32
			var deadline atomic.Int64
			entered := make(chan struct{})
			address, _ := userAPI(t, userAdditionServer{
				list: func(ctx context.Context, _ *handlers.ListInboundsRequest) (*handlers.ListInboundsResponse, error) {
					value, _ := ctx.Deadline()
					deadline.Store(value.UnixNano())
					return &handlers.ListInboundsResponse{Inbounds: []*core.InboundHandlerConfig{additionTarget()}}, nil
				},
				add: func(ctx context.Context, _ *handlers.AlterInboundRequest) (*handlers.AlterInboundResponse, error) {
					value, _ := ctx.Deadline()
					if value.After(time.Unix(0, deadline.Load()).Add(10 * time.Millisecond)) {
						t.Error("collection renewed its native deadline")
					}
					if adds.Add(1) == 1 {
						return &handlers.AlterInboundResponse{}, nil
					}
					// The second user can be installed before its acknowledgement is lost.
					close(entered)
					<-ctx.Done()
					return nil, status.FromContextError(ctx.Err()).Err()
				},
			})
			client := userClient(t, address, 500*time.Millisecond, 4096)
			ctx, cancel := context.WithCancel(t.Context())
			finished := make(chan struct{})
			var report domain.UserAdditionsReport
			var failure error
			go func() { report, failure = client.AddInboundUsers(ctx, userAdditions(3)); close(finished) }()
			defer func() { cancel(); _ = client.Close(); <-finished }()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("second addition did not reach native execution")
			}
			if _, err := client.ReadRuntimeInfo(t.Context()); !errors.Is(err, engineapi.ErrBusy) {
				t.Fatal("collection bypassed shared admission")
			}
			if rejected, err := client.AddInboundUsers(t.Context(), userAdditions(1)); !errors.Is(err, engineapi.ErrBusy) || len(rejected.Items) != 0 || errors.Is(err, domain.ErrUserMutationUnknown) {
				t.Fatal("saturated collection was queued or acquired uncertain effects")
			}
			if mode == "cancel" {
				cancel()
			} else if mode == "close" {
				if err := client.Close(); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case <-finished:
			case <-time.After(3 * time.Second):
				t.Fatal("interrupted collection failed to join")
			}
			if !errors.Is(failure, domain.ErrUserMutationUnknown) || adds.Load() != 2 || len(report.Items) != 3 || report.Items[0].State != domain.UserMutationAcknowledged || report.Items[1].State != domain.UserMutationUnknown || report.Items[2].State != domain.UserMutationNotAttempted {
				t.Fatal("interrupted collection lost its actual progress")
			}
			if mode == "cancel" && !errors.Is(failure, context.Canceled) || mode == "timeout" && !errors.Is(failure, context.DeadlineExceeded) {
				t.Fatal("caller interruption cause was lost")
			}
		})
	}
}
