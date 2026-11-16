//go:build integration

package grpcapi_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/fx"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/PapilioVPN/node/internal/bootstrap"
	xrayv1 "github.com/PapilioVPN/node/internal/bootstrap/xrayapi/proto/v1"
	"github.com/PapilioVPN/node/internal/modules/xray/users"
	"github.com/PapilioVPN/node/internal/modules/xray/users/app/services"
	"github.com/PapilioVPN/node/internal/modules/xray/users/domain"
	userv1 "github.com/PapilioVPN/node/internal/modules/xray/users/grpcapi/proto/v1"
	rpc "github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/shared/testutil"
)

type controlledDelta struct{ calls atomic.Int32 }

func (c *controlledDelta) ApplyInboundUserDelta(_ context.Context, input domain.InboundUserDelta) (domain.UserDeltaReport, error) {
	c.calls.Add(1)
	report := domain.UserDeltaReport{ObservedAt: time.UnixMilli(0), Steps: make([]domain.UserMutationStep, len(input.Users))}
	for i, user := range input.Users {
		report.Steps[i] = domain.UserMutationStep{Email: user.Email, Action: domain.UserMutationRemove, State: domain.UserMutationAcknowledged, AcknowledgedAt: time.UnixMilli(0)}
	}
	if len(input.Users) == 0 {
		return report, nil
	}
	cause := errors.Join(domain.ErrUserMutationUnknown, errors.New("private-native-cause"))
	report.Steps[len(report.Steps)-1].State = domain.UserMutationUnknown
	report.Steps[len(report.Steps)-1].AcknowledgedAt = time.Time{}
	report.Steps[len(report.Steps)-1].Failure = cause
	return report, cause
}

// Controlled application results prove actual mTLS/status-details fidelity.
// Real membership, native failures and VLESS authentication are separate tests.
func TestUserDeltaProgressOverMTLSRetainsLargeReportsAndCombinedBudget(t *testing.T) {
	certificates := testutil.NewCertificates(t)
	cfg := certificates.Config()
	cfg.Communications.GRPC.MaxReceiveBytes = 512 * 1024
	cfg.Communications.GRPC.Permissions.ManageEngineUsers = true
	backend := &controlledDelta{}
	var server *rpc.Server
	application := bootstrap.New(cfg, "test", fx.NopLogger, fx.Provide(func() userCollectionBindings {
		return userCollectionBindings{Values: []users.BackendBinding{{InstanceID: "controlled", Delta: services.UserDeltaBinding{Applier: backend, MaxUsers: 1000}}}}
	}), fx.Populate(&server))
	if err := application.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Stop(context.Background()); _ = server.Close() })
	client := xrayv1.NewXrayServiceClient(certificates.GRPCClient(t, server.Addr().String()))
	for _, count := range []int{0, 1, 100, 1000} {
		request := &userv1.ApplyInboundUserDeltaRequest{InstanceId: "controlled", InboundTag: " exact ", Users: make([]*userv1.InboundUser, count)}
		for i := range request.Users {
			request.Users[i] = &userv1.InboundUser{Email: fmt.Sprint(" user-", i, " "), Account: &userv1.InboundUser_Trojan{Trojan: &userv1.PasswordAccount{Password: "private-input-password"}}}
		}
		before := backend.calls.Load()
		response, err := client.ApplyInboundUserDelta(t.Context(), request)
		if backend.calls.Load() != before+1 {
			t.Fatal("wire delta split or replayed the application call")
		}
		if count == 0 {
			if err != nil || response.ObservedAtUnixMs == nil || response.GetObservedAtUnixMs() != 0 || len(response.Steps) != 0 {
				t.Fatal("empty controlled delta lost timestamp presence")
			}
			continue
		}
		if response != nil || status.Code(err) != codes.Unavailable {
			t.Fatal("wire delta turned a partial failure into success")
		}
		details := status.Convert(err).Details()
		if len(details) != 1 {
			t.Fatal("wire delta lost progress details")
		}
		failure, ok := details[0].(*userv1.UserDeltaFailure)
		if !ok || failure.Outcome != userv1.UserMutationOutcome_USER_MUTATION_OUTCOME_UNKNOWN || len(failure.Progress.Steps) != count || failure.Progress.ObservedAtUnixMs == nil || failure.Progress.GetObservedAtUnixMs() != 0 {
			t.Fatal("large wire delta truncated progress or lost observation presence")
		}
		for i, step := range failure.Progress.Steps {
			want := userv1.UserDeltaState_USER_DELTA_STATE_ACKNOWLEDGED
			if i == count-1 {
				want = userv1.UserDeltaState_USER_DELTA_STATE_UNKNOWN
			}
			if step.Email != request.Users[i].Email || step.Action != userv1.UserDeltaAction_USER_DELTA_ACTION_REMOVE || step.State != want || (step.AcknowledgedAtUnixMs != nil) != (i < count-1) || step.GetAcknowledgedAtUnixMs() != 0 {
				t.Fatal("wire delta changed exact ordered receipts")
			}
		}
		encoded, marshalErr := proto.Marshal(status.Convert(err).Proto())
		if marshalErr != nil || strings.Contains(string(encoded), "private-input-password") || strings.Contains(string(encoded), "private-native-cause") {
			t.Fatal("wire error details exposed credentials or native causes")
		}
		if count == 1000 {
			request.RemoveEmails = []string{"extra"}
			response, err = client.ApplyInboundUserDelta(t.Context(), request)
			if response != nil || status.Code(err) != codes.ResourceExhausted || backend.calls.Load() != before+1 {
				t.Fatal("removal identities bypassed the configured aggregate budget")
			}
			details := status.Convert(err).Details()
			if len(details) != 1 {
				t.Fatal("budget rejection lost known no-effect detail")
			}
			failure, ok := details[0].(*userv1.UserDeltaFailure)
			if !ok || failure.Outcome != userv1.UserMutationOutcome_USER_MUTATION_OUTCOME_NOT_APPLIED || failure.Progress.ObservedAtUnixMs != nil || len(failure.Progress.Steps) != 0 {
				t.Fatal("budget rejection claimed a native observation/effect")
			}
		}
	}
}
