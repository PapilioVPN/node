package mutations_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	servicegrpc "github.com/PapilioVPN/node/internal/bootstrap/xrayapi"
	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	online "github.com/PapilioVPN/node/internal/modules/xray/online/grpcapi"
	"github.com/PapilioVPN/node/internal/modules/xray/users/domain"
	"github.com/PapilioVPN/node/internal/modules/xray/users/grpcapi"
	"github.com/PapilioVPN/node/internal/modules/xray/users/grpcapi/mutations"
	"github.com/PapilioVPN/node/internal/modules/xray/users/grpcapi/observations"
	userv1 "github.com/PapilioVPN/node/internal/modules/xray/users/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/shared/testutil"
)

type usersAdder func(context.Context, instances.InstanceID, domain.InboundUserAdditions) (domain.InstanceInboundUserAdditions, error)

func (f usersAdder) AddInboundUsers(ctx context.Context, id instances.InstanceID, input domain.InboundUserAdditions) (domain.InstanceInboundUserAdditions, error) {
	return f(ctx, id, input)
}

func TestUserAdditionsVerifyIdentityAndWriteGrantBeforeShape(t *testing.T) {
	if _, err := (grpcapi.UserAPI{MutationAPI: mutations.MutationAPI{AllowUserManagement: true}}).AddInboundUsers(t.Context(), nil); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("identity did not precede collection decoding: %v", err)
	}
	if _, err := (servicegrpc.Handler{UserAPI: grpcapi.UserAPI{ObservationAPI: observations.ObservationAPI{AllowUsersRead: true, AllowUserAccountsRead: true}}, OnlineAPI: online.OnlineAPI{AllowOnlineRead: true}}).AddInboundUsers(testutil.VerifiedGRPCContext(t), nil); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("read grants enabled collection mutations: %v", err)
	}
}

func TestUserAdditionsPreserveEveryProgressEntryAndExcludePrivateCauses(t *testing.T) {
	for _, count := range []int{0, 1, 100, 1000} {
		for _, fail := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/fail=%v", count, fail), func(t *testing.T) {
				request := &userv1.AddInboundUsersRequest{InstanceId: " edge ", InboundTag: " opaque>>>tag ", Users: make([]*userv1.InboundUser, count)}
				report := domain.UserAdditionsReport{Items: make([]domain.UserAdditionResult, count)}
				states := []domain.UserMutationState{domain.UserMutationAcknowledged, domain.UserMutationUnknown, domain.UserMutationNotApplied, domain.UserMutationNotAttempted}
				wireStates := []userv1.UserAdditionState{userv1.UserAdditionState_USER_ADDITION_STATE_ACKNOWLEDGED, userv1.UserAdditionState_USER_ADDITION_STATE_UNKNOWN, userv1.UserAdditionState_USER_ADDITION_STATE_NOT_APPLIED, userv1.UserAdditionState_USER_ADDITION_STATE_NOT_ATTEMPTED}
				for i := range request.Users {
					email := fmt.Sprint(" repeated-", i%7, " ")
					request.Users[i] = &userv1.InboundUser{Email: email, Account: &userv1.InboundUser_Trojan{Trojan: &userv1.PasswordAccount{Password: "private-input-credential"}}}
					state := domain.UserMutationAcknowledged
					if fail {
						state = states[i%len(states)]
					}
					report.Items[i] = domain.UserAdditionResult{Email: email, State: state}
					if state == domain.UserMutationAcknowledged {
						report.Items[i].AcknowledgedAt = time.UnixMilli(0)
					} else if state != domain.UserMutationNotAttempted {
						report.Items[i].Failure = errors.New("private-native-cause")
					}
				}
				ctx, cancel := context.WithCancel(testutil.VerifiedGRPCContext(t))
				defer cancel()
				calls := 0
				h := grpcapi.UserAPI{MutationAPI: mutations.MutationAPI{AllowUserManagement: true, UserAdditions: usersAdder(func(received context.Context, id instances.InstanceID, input domain.InboundUserAdditions) (domain.InstanceInboundUserAdditions, error) {
					calls++
					if received != ctx || id != " edge " || input.InboundTag != request.InboundTag || len(input.Users) != count {
						t.Fatal("collection selection/context changed")
					}
					for i, user := range input.Users {
						if user.Email != request.Users[i].Email || user.Account.Trojan.Password != "private-input-credential" {
							t.Fatal("decoded collection changed identity/order/credentials")
						}
						request.Users[i].Email = "consumed" // The result must come from the port.
					}
					result := domain.InstanceInboundUserAdditions{InstanceID: id, InboundTag: input.InboundTag, Report: report}
					if fail {
						return result, errors.Join(domain.ErrUserManagementUnsupported, errors.New("private-native-cause"))
					}
					cancel()
					return result, nil
				})}}
				response, err := h.AddInboundUsers(ctx, request)
				if calls != 1 {
					t.Fatal("collection application call was split or replayed")
				}
				if fail {
					if response != nil || status.Code(err) != codes.FailedPrecondition || strings.Contains(err.Error(), "private") {
						t.Fatal("failed collection returned success or exposed native diagnostics")
					}
					details := status.Convert(err).Details()
					if len(details) != 1 {
						t.Fatal("collection progress missing")
					}
					failure, ok := details[0].(*userv1.UserAdditionsFailure)
					if !ok {
						t.Fatal("single-user certainty replaced collection progress")
					}
					want := userv1.UserMutationOutcome_USER_MUTATION_OUTCOME_NOT_APPLIED
					if count > 0 {
						want = userv1.UserMutationOutcome_USER_MUTATION_OUTCOME_UNKNOWN
					}
					if failure.Outcome != want {
						t.Fatal("a later unsupported RPC erased earlier effects")
					}
					response = failure.Progress
				} else if err != nil {
					t.Fatal("late cancellation discarded completed collection")
				}
				if response.InstanceId != " edge " || response.InboundTag != request.InboundTag || len(response.Items) != count {
					t.Fatal("collection output lost selection/cardinality")
				}
				for i, item := range response.Items {
					state := userv1.UserAdditionState_USER_ADDITION_STATE_ACKNOWLEDGED
					if fail {
						state = wireStates[i%len(wireStates)]
					}
					if item.Email != report.Items[i].Email || item.State != state || (item.AcknowledgedAtUnixMs != nil) != (state == userv1.UserAdditionState_USER_ADDITION_STATE_ACKNOWLEDGED) || item.GetAcknowledgedAtUnixMs() != 0 {
						t.Fatal("progress lost exact order, state or epoch-zero timestamp presence")
					}
				}
				encoded, err := proto.Marshal(response)
				if err != nil || strings.Contains(string(encoded), "private") {
					t.Fatal("collection output exposed account/native diagnostic data")
				}
			})
		}
	}
}

func TestUserAdditionsRejectInvalidTrailingAccountsBeforeApplication(t *testing.T) {
	h := grpcapi.UserAPI{MutationAPI: mutations.MutationAPI{AllowUserManagement: true}}
	valid := &userv1.InboundUser{Email: "user", Account: &userv1.InboundUser_Vless{Vless: &userv1.VLESSAccount{Id: "test-only"}}}
	for _, request := range []*userv1.AddInboundUsersRequest{nil, {}, {InstanceId: "edge", InboundTag: " "}, {InstanceId: " ", InboundTag: "in"}, {InstanceId: "edge", InboundTag: "in", Users: []*userv1.InboundUser{valid, nil}}} {
		if _, err := h.AddInboundUsers(testutil.VerifiedGRPCContext(t), request); status.Code(err) != codes.InvalidArgument {
			t.Fatal("malformed collection reached application")
		}
	}
	ctx, cancel := context.WithCancel(testutil.VerifiedGRPCContext(t))
	cancel()
	if _, err := h.AddInboundUsers(ctx, &userv1.AddInboundUsersRequest{InstanceId: "edge", InboundTag: "in"}); status.Code(err) != codes.Canceled {
		t.Fatal("canceled empty collection reached application")
	}
}
