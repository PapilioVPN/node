//go:build integration

package mutations_test

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

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	userv1 "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/bootstrap"
	xrayservice "github.com/PapilioVPN/node/internal/bootstrap/xrayapi/proto/v1"
	rpc "github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/testutil"
)

type userCollectionBindings struct {
	fx.Out
	Values []accounts.BackendBinding `group:"user_backends,flatten"`
}

type controlledUsersAdder struct{ calls atomic.Int32 }

func (a *controlledUsersAdder) AddInboundUsers(_ context.Context, input domain.InboundUserAdditions) (domain.UserAdditionsReport, error) {
	a.calls.Add(1)
	report := domain.UserAdditionsReport{Items: make([]domain.UserAdditionResult, len(input.Users))}
	for i, user := range input.Users {
		report.Items[i] = domain.UserAdditionResult{Email: user.Email, State: domain.UserMutationAcknowledged, AcknowledgedAt: time.UnixMilli(0)}
	}
	if len(report.Items) == 0 {
		return report, nil
	}
	cause := errors.Join(domain.ErrUserMutationUnknown, errors.New("private-native-credential"))
	report.Items[len(report.Items)-1] = domain.UserAdditionResult{Email: input.Users[len(input.Users)-1].Email, State: domain.UserMutationUnknown, Failure: cause}
	return report, cause
}

// Controlled application results verify the actual mTLS/status-details wire path.
// Real engine membership, account families and traffic are covered separately.
func TestUserCollectionProgressOverMTLSRetainsLargeReports(t *testing.T) {
	certificates := testutil.NewCertificates(t)
	cfg := certificates.Config()
	cfg.Communications.GRPC.MaxReceiveBytes = 512 * 1024
	cfg.Communications.GRPC.Permissions.ManageEngineUsers = true
	adder := &controlledUsersAdder{}
	var server *rpc.Server
	application := bootstrap.New(cfg, "test", fx.NopLogger, fx.Provide(func() userCollectionBindings {
		return userCollectionBindings{Values: []accounts.BackendBinding{{InstanceID: "controlled", Additions: services.UserAdditionsBinding{Adder: adder, MaxUsers: 1000}}}}
	}), fx.Populate(&server))
	if err := application.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Stop(context.Background()); _ = server.Close() })
	client := xrayservice.NewXrayServiceClient(certificates.GRPCClient(t, server.Addr().String()))
	for _, count := range []int{0, 1, 100, 1000} {
		request := &userv1.AddInboundUsersRequest{InstanceId: "controlled", InboundTag: " exact>>>tag ", Users: make([]*userv1.InboundUser, count)}
		for i := range request.Users {
			request.Users[i] = &userv1.InboundUser{Email: fmt.Sprint(" repeated-", i%7, " "), Account: &userv1.InboundUser_Trojan{Trojan: &userv1.PasswordAccount{Password: "private-input-credential"}}}
		}
		before := adder.calls.Load()
		response, err := client.AddInboundUsers(t.Context(), request)
		if adder.calls.Load() != before+1 {
			t.Fatal("wire collection split or replayed application work")
		}
		if count == 0 {
			if err != nil || response == nil || len(response.Items) != 0 {
				t.Fatal("empty wire collection was not a completed no-op")
			}
			continue
		}
		if response != nil || status.Code(err) != codes.Unavailable || strings.Contains(err.Error(), "private") {
			t.Fatal("partial wire failure was lost or exposed diagnostics")
		}
		details := status.Convert(err).Details()
		if len(details) != 1 {
			t.Fatal("large progress report was dropped from gRPC details")
		}
		failure, ok := details[0].(*userv1.UserAdditionsFailure)
		if !ok || failure.Progress == nil || failure.Outcome != userv1.UserMutationOutcome_USER_MUTATION_OUTCOME_UNKNOWN || len(failure.Progress.Items) != count {
			t.Fatal("large progress report changed certainty/cardinality")
		}
		for i, item := range failure.Progress.Items {
			state := userv1.UserAdditionState_USER_ADDITION_STATE_ACKNOWLEDGED
			if i == count-1 {
				state = userv1.UserAdditionState_USER_ADDITION_STATE_UNKNOWN
			}
			if item.Email != request.Users[i].Email || item.State != state || (item.AcknowledgedAtUnixMs != nil) != (i != count-1) || item.GetAcknowledgedAtUnixMs() != 0 {
				t.Fatal("large wire progress lost ordering, duplicates or timestamp presence")
			}
		}
		encoded, encodeError := proto.Marshal(failure)
		if encodeError != nil || strings.Contains(string(encoded), "private") {
			t.Fatal("wire progress disclosed credentials or native diagnostics")
		}
	}
}
