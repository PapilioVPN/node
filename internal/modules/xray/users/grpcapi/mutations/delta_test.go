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
	"github.com/PapilioVPN/node/internal/testutil"
)

func TestUserDeltaChecksIdentityAndWriteGrantBeforeDecoding(t *testing.T) {
	if _, err := (grpcapi.UserAPI{MutationAPI: mutations.MutationAPI{AllowUserManagement: true}}).ApplyInboundUserDelta(t.Context(), nil); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("delta did not verify identity before decoding: %v", err)
	}
	if _, err := (servicegrpc.Handler{UserAPI: grpcapi.UserAPI{ObservationAPI: observations.ObservationAPI{AllowUsersRead: true, AllowUserAccountsRead: true}}, OnlineAPI: online.OnlineAPI{AllowOnlineRead: true}}).ApplyInboundUserDelta(testutil.VerifiedGRPCContext(t), nil); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("other grants enabled native delta effects: %v", err)
	}
}

type inboundDelta func(context.Context, instances.InstanceID, domain.InboundUserDelta) (domain.InstanceInboundUserDelta, error)

func (f inboundDelta) ApplyInboundUserDelta(ctx context.Context, id instances.InstanceID, input domain.InboundUserDelta) (domain.InstanceInboundUserDelta, error) {
	return f(ctx, id, input)
}

func TestUserDeltaWirePreservesCompleteProgressWithoutCredentials(t *testing.T) {
	for _, count := range []int{0, 1, 100, 1000} {
		for _, fail := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/fail=%v", count, fail), func(t *testing.T) {
				request := &userv1.ApplyInboundUserDeltaRequest{InstanceId: " edge ", InboundTag: " exact ", Users: make([]*userv1.InboundUser, count), RemoveEmails: []string{" removed "}}
				report := domain.UserDeltaReport{ObservedAt: time.UnixMilli(0), Steps: make([]domain.UserMutationStep, count)}
				states := []domain.UserMutationState{domain.UserMutationAcknowledged, domain.UserMutationUnknown, domain.UserMutationNotApplied, domain.UserMutationNotAttempted, domain.UserMutationNotRequired}
				wireStates := []userv1.UserDeltaState{userv1.UserDeltaState_USER_DELTA_STATE_ACKNOWLEDGED, userv1.UserDeltaState_USER_DELTA_STATE_UNKNOWN, userv1.UserDeltaState_USER_DELTA_STATE_NOT_APPLIED, userv1.UserDeltaState_USER_DELTA_STATE_NOT_ATTEMPTED, userv1.UserDeltaState_USER_DELTA_STATE_NOT_REQUIRED}
				for i := range count {
					email := fmt.Sprint(" user-", i, " ")
					request.Users[i] = &userv1.InboundUser{Email: email, Account: &userv1.InboundUser_Trojan{Trojan: &userv1.PasswordAccount{Password: "secret-input-password"}}}
					state := domain.UserMutationAcknowledged
					if fail {
						state = states[i%len(states)]
					}
					report.Steps[i] = domain.UserMutationStep{Email: email, Action: domain.UserMutationRemove, State: state, Failure: errors.New("private-native-cause")}
					if state == domain.UserMutationAcknowledged {
						report.Steps[i].AcknowledgedAt = time.UnixMilli(0)
						report.Steps[i].Action = domain.UserMutationAdd
					}
				}
				ctx, cancel := context.WithCancel(testutil.VerifiedGRPCContext(t))
				defer cancel()
				calls := 0
				handler := grpcapi.UserAPI{MutationAPI: mutations.MutationAPI{AllowUserManagement: true, UserDelta: inboundDelta(func(received context.Context, id instances.InstanceID, input domain.InboundUserDelta) (domain.InstanceInboundUserDelta, error) {
					calls++
					if received != ctx || id != " edge " || input.InboundTag != request.InboundTag || len(input.Users) != count || len(input.RemoveEmails) != 1 || input.RemoveEmails[0] != " removed " {
						t.Fatal("delta lost selection/context/removal bytes")
					}
					for i, user := range input.Users {
						if user.Email != request.Users[i].Email || user.Account.Trojan.Password != "secret-input-password" {
							t.Fatal("delta lost typed account fields")
						}
					}
					result := domain.InstanceInboundUserDelta{InstanceID: id, InboundTag: input.InboundTag, Report: report}
					if fail {
						return result, errors.Join(domain.ErrUserReadUnsupported, errors.New("private-native-cause"))
					}
					cancel()
					return result, nil
				})}}
				response, err := handler.ApplyInboundUserDelta(ctx, request)
				if calls != 1 {
					t.Fatal("delta split/replayed application work")
				}
				if fail {
					if response != nil || status.Code(err) != codes.FailedPrecondition || strings.Contains(err.Error(), "private-native-cause") {
						t.Fatal("failed delta lost status or exposed native diagnostics")
					}
					details := status.Convert(err).Details()
					if len(details) != 1 {
						t.Fatal("delta lost progress details")
					}
					failure, ok := details[0].(*userv1.UserDeltaFailure)
					if !ok {
						t.Fatal("delta became a single-user outcome")
					}
					want := userv1.UserMutationOutcome_USER_MUTATION_OUTCOME_NOT_APPLIED
					if count > 0 {
						want = userv1.UserMutationOutcome_USER_MUTATION_OUTCOME_UNKNOWN
					}
					if failure.Outcome != want {
						t.Fatal("a later unsupported operation erased earlier effects")
					}
					response = failure.Progress
				} else if err != nil {
					t.Fatal("late cancellation erased completed delta")
				}
				if response.InstanceId != " edge " || response.InboundTag != request.InboundTag || response.ObservedAtUnixMs == nil || response.GetObservedAtUnixMs() != 0 || len(response.Steps) != count {
					t.Fatal("delta lost optional observation/identity/cardinality")
				}
				for i, step := range response.Steps {
					state := userv1.UserDeltaState_USER_DELTA_STATE_ACKNOWLEDGED
					if fail {
						state = wireStates[i%len(wireStates)]
					}
					action := userv1.UserDeltaAction_USER_DELTA_ACTION_REMOVE
					if state == userv1.UserDeltaState_USER_DELTA_STATE_ACKNOWLEDGED {
						action = userv1.UserDeltaAction_USER_DELTA_ACTION_ADD
					}
					if step.Email != report.Steps[i].Email || step.State != state || step.Action != action || (step.AcknowledgedAtUnixMs != nil) != (state == userv1.UserDeltaState_USER_DELTA_STATE_ACKNOWLEDGED) || step.GetAcknowledgedAtUnixMs() != 0 {
						t.Fatal("delta changed ordered step/action/state/receipt presence")
					}
				}
				encoded, err := proto.Marshal(response)
				if err != nil || strings.Contains(string(encoded), "secret-input-password") || strings.Contains(string(encoded), "private-native-cause") {
					t.Fatal("delta progress exposed credentials or private causes")
				}
			})
		}
	}
}

func TestUserDeltaRejectsInvalidTrailingShapeBeforeApplication(t *testing.T) {
	h := grpcapi.UserAPI{MutationAPI: mutations.MutationAPI{AllowUserManagement: true}}
	valid := &userv1.InboundUser{Email: "user", Account: &userv1.InboundUser_Vless{Vless: &userv1.VLESSAccount{Id: "native-id"}}}
	for _, request := range []*userv1.ApplyInboundUserDeltaRequest{nil, {}, {InstanceId: "edge", InboundTag: " "}, {InstanceId: " ", InboundTag: "in"}, {InstanceId: "edge", InboundTag: "in", Users: []*userv1.InboundUser{valid, nil}}, {InstanceId: "edge", InboundTag: "in", RemoveEmails: []string{"valid", " "}}, {InstanceId: "edge", InboundTag: "in", RemoveEmails: []string{string([]byte{0xff})}}} {
		if _, err := h.ApplyInboundUserDelta(testutil.VerifiedGRPCContext(t), request); status.Code(err) != codes.InvalidArgument {
			t.Fatal("malformed delta reached application")
		}
	}
	ctx, cancel := context.WithCancel(testutil.VerifiedGRPCContext(t))
	cancel()
	if _, err := h.ApplyInboundUserDelta(ctx, &userv1.ApplyInboundUserDeltaRequest{InstanceId: "edge", InboundTag: "in"}); status.Code(err) != codes.Canceled {
		t.Fatal("canceled empty delta reached application")
	}
}
