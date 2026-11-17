//go:build integration

package api_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	native "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/core"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
)

// Controlled SDK responses establish request growth and effects, not real auth.
type userDeltaServer struct {
	userAdditionServer
	users func(context.Context, *native.GetInboundUserRequest) (*native.GetInboundUserResponse, error)
}

func (s userDeltaServer) GetInboundUsers(ctx context.Context, r *native.GetInboundUserRequest) (*native.GetInboundUserResponse, error) {
	return s.users(ctx, r)
}

func TestUserDeltaUsesOneSnapshotAndOnePreflightAtEveryCollectionSize(t *testing.T) {
	for _, count := range []int{0, 1, 100, 1000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			var lists, reads, removes, adds atomic.Int32
			input := domain.InboundUserDelta{InboundTag: userAddition().InboundTag, Users: make([]domain.InboundUser, count)}
			members := make([]*protocol.User, count+1)
			members[count] = &protocol.User{Email: "unselected"}
			for i := range count {
				input.Users[i] = deltaUser(fmt.Sprintf("Selected-%d", i))
				members[i] = &protocol.User{Email: fmt.Sprintf("selected-%d", i)}
			}
			address, connections := userAPI(t, userDeltaServer{
				userAdditionServer: userAdditionServer{
					list: func(context.Context, *native.ListInboundsRequest) (*native.ListInboundsResponse, error) {
						lists.Add(1)
						return &native.ListInboundsResponse{Inbounds: []*core.InboundHandlerConfig{additionTarget()}}, nil
					},
					add: func(_ context.Context, r *native.AlterInboundRequest) (*native.AlterInboundResponse, error) {
						op, err := r.Operation.GetInstance()
						if err != nil {
							return nil, err
						}
						switch v := op.(type) {
						case *native.RemoveUserOperation:
							i := int(removes.Add(1)) - 1
							if v.Email != input.Users[i].Email {
								t.Error("native removal lost input order/bytes")
							}
						case *native.AddUserOperation:
							i := int(adds.Add(1)) - 1
							if v.User.Email != input.Users[i].Email || v.User.Level != 31 {
								t.Error("native addition lost identity/level")
							}
						default:
							t.Error("unexpected native delta operation")
						}
						return &native.AlterInboundResponse{}, nil
					},
				},
				users: func(_ context.Context, r *native.GetInboundUserRequest) (*native.GetInboundUserResponse, error) {
					reads.Add(1)
					if r.Email != "" || r.Tag != input.InboundTag {
						t.Error("delta used per-user reads or wrong tag")
					}
					return &native.GetInboundUserResponse{Users: members}, nil
				},
			})
			report, err := userClient(t, address, 10*time.Second, 128*1024).ApplyInboundUserDelta(t.Context(), input)
			expected := int32(1)
			if count == 0 {
				expected = 0
			}
			if err != nil || len(report.Steps) != 2*count || lists.Load() != expected || reads.Load() != expected || removes.Load() != int32(count) || adds.Load() != int32(count) || connections.Opened.Load() != expected {
				t.Fatalf("delta added reads/channels or lost effects: err=%v steps=%d lists=%d reads=%d removes=%d adds=%d channels=%d", err, len(report.Steps), lists.Load(), reads.Load(), removes.Load(), adds.Load(), connections.Opened.Load())
			}
			for i, step := range report.Steps {
				if step.State != domain.UserMutationAcknowledged || step.AcknowledgedAt.IsZero() || step.Email != input.Users[i/2].Email || step.Action != domain.UserMutationAction(i%2) {
					t.Fatalf("delta lost exact progress at step %d", i)
				}
			}
		})
	}
}

func TestUserDeltaNativeReadFailureHasNoMutationOrSuccessfulObservation(t *testing.T) {
	for _, code := range []codes.Code{codes.Unimplemented, codes.Unavailable, codes.ResourceExhausted, codes.Canceled, codes.DeadlineExceeded} {
		t.Run(code.String(), func(t *testing.T) {
			var writes atomic.Int32
			address, _ := userAPI(t, userDeltaServer{userAdditionServer: userAdditionServer{
				list: func(context.Context, *native.ListInboundsRequest) (*native.ListInboundsResponse, error) {
					return &native.ListInboundsResponse{Inbounds: []*core.InboundHandlerConfig{additionTarget()}}, nil
				},
				add: func(context.Context, *native.AlterInboundRequest) (*native.AlterInboundResponse, error) {
					writes.Add(1)
					return &native.AlterInboundResponse{}, nil
				},
			}, users: func(context.Context, *native.GetInboundUserRequest) (*native.GetInboundUserResponse, error) {
				return nil, status.Error(code, "private native diagnostics")
			}})
			report, err := userClient(t, address, time.Second, 4096).ApplyInboundUserDelta(t.Context(), domain.InboundUserDelta{InboundTag: userAddition().InboundTag, Users: []domain.InboundUser{deltaUser("selected")}})
			if status.Code(err) != code || errors.Is(err, domain.ErrUserMutationUnknown) || writes.Load() != 0 || len(report.Steps) != 0 || !report.ObservedAt.IsZero() {
				t.Fatalf("failed observation reached native mutation or became successful: %v", err)
			}
		})
	}
}

func TestUserDeltaCancellationDeadlineAndCloseJoinPartialEffects(t *testing.T) {
	for _, mode := range []string{"cancel", "timeout", "close"} {
		t.Run(mode, func(t *testing.T) {
			entered := make(chan struct{})
			var writes atomic.Int32
			var deadline atomic.Int64
			address, _ := userAPI(t, userDeltaServer{userAdditionServer: userAdditionServer{
				list: func(ctx context.Context, _ *native.ListInboundsRequest) (*native.ListInboundsResponse, error) {
					d, _ := ctx.Deadline()
					deadline.Store(d.UnixNano())
					return &native.ListInboundsResponse{Inbounds: []*core.InboundHandlerConfig{additionTarget()}}, nil
				},
				add: func(ctx context.Context, _ *native.AlterInboundRequest) (*native.AlterInboundResponse, error) {
					d, _ := ctx.Deadline()
					if d.After(time.Unix(0, deadline.Load()).Add(10 * time.Millisecond)) {
						t.Error("delta renewed its native budget")
					}
					if writes.Add(1) == 1 {
						return &native.AlterInboundResponse{}, nil
					}
					close(entered)
					<-ctx.Done()
					return nil, status.FromContextError(ctx.Err()).Err()
				},
			}, users: func(context.Context, *native.GetInboundUserRequest) (*native.GetInboundUserResponse, error) {
				return &native.GetInboundUserResponse{Users: []*protocol.User{{Email: "selected"}}}, nil
			}})
			client := userClient(t, address, 500*time.Millisecond, 4096)
			ctx, cancel := context.WithCancel(t.Context())
			finished := make(chan struct{})
			var report domain.UserDeltaReport
			var failure error
			go func() {
				report, failure = client.ApplyInboundUserDelta(ctx, domain.InboundUserDelta{InboundTag: userAddition().InboundTag, Users: []domain.InboundUser{deltaUser("selected"), deltaUser("trailing")}})
				close(finished)
			}()
			defer func() { cancel(); _ = client.Close(); <-finished }()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("native replacement did not reach addition")
			}
			if _, err := client.ReadRuntimeInfo(t.Context()); !errors.Is(err, domain.ErrAPIBusy) {
				t.Fatal("delta bypassed shared native capacity")
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
				t.Fatal("delta did not join interrupted native work")
			}
			if !errors.Is(failure, domain.ErrUserMutationUnknown) || len(report.Steps) != 3 || report.Steps[0].State != domain.UserMutationAcknowledged || report.Steps[1].State != domain.UserMutationUnknown || report.Steps[2].State != domain.UserMutationNotAttempted || writes.Load() != 2 {
				t.Fatal("delta hid acknowledged removal or replayed interrupted addition")
			}
			if mode == "cancel" && !errors.Is(failure, context.Canceled) || mode == "timeout" && !errors.Is(failure, context.DeadlineExceeded) {
				t.Fatal("delta discarded caller interruption")
			}
		})
	}
}
