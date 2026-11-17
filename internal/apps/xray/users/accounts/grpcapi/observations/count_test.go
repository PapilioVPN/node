package observations_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	instances "github.com/PapilioVPN/node/internal/apps/core/engines/instances/domain"
	trafficgrpc "github.com/PapilioVPN/node/internal/apps/xray/network/traffic/grpcapi"
	infogrpc "github.com/PapilioVPN/node/internal/apps/xray/runtime/info/grpcapi"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	usergrpc "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/grpcapi"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/grpcapi/mutations"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/grpcapi/observations"
	userv1 "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/grpcapi/proto/v1"
	online "github.com/PapilioVPN/node/internal/apps/xray/users/online/grpcapi"
	grpcapi "github.com/PapilioVPN/node/internal/bootstrap/xrayapi"
	"github.com/PapilioVPN/node/internal/testutil"
)

type inboundUserCounter func(context.Context, instances.InstanceID, string) (domain.InstanceInboundUserCount, error)

func (f inboundUserCounter) GetInboundUserCount(ctx context.Context, id instances.InstanceID, tag string) (domain.InstanceInboundUserCount, error) {
	return f(ctx, id, tag)
}

func TestUserCountRequiresIdentityAndItsOwnGrantBeforeShape(t *testing.T) {
	if _, err := (grpcapi.Handler{}).GetInboundUserCount(t.Context(), nil); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("unverified count request reached shape/application: %v", err)
	}
	if _, err := (grpcapi.Handler{UserAPI: usergrpc.UserAPI{MutationAPI: mutations.MutationAPI{AllowUserManagement: true}}, OnlineAPI: online.OnlineAPI{AllowOnlineRead: true}, TrafficAPI: trafficgrpc.TrafficAPI{AllowStatsRead: true, AllowStatsReset: true}, InfoAPI: infogrpc.InfoAPI{AllowInfoRead: true}}).GetInboundUserCount(testutil.VerifiedGRPCContext(t), nil); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("other grants enabled user counts: %v", err)
	}
}

func TestUserCountPreservesOpaqueIdentityZeroAndFullNativePrecision(t *testing.T) {
	for _, count := range []int64{0, math.MaxInt64} {
		ctx := testutil.VerifiedGRPCContext(t)
		observed := time.UnixMilli(1700000000123)
		calls := 0
		h := grpcapi.Handler{UserAPI: usergrpc.UserAPI{ObservationAPI: observations.ObservationAPI{AllowUsersRead: true, UserCounts: inboundUserCounter(func(received context.Context, id instances.InstanceID, tag string) (domain.InstanceInboundUserCount, error) {
			calls++
			if received != ctx || id != " edge " || tag != " exact>>>tag " {
				t.Fatal("count selection/context changed")
			}
			return domain.InstanceInboundUserCount{InstanceID: id, InboundTag: tag, Users: domain.InboundUserCount{Count: count, ObservedAt: observed}}, nil
		})}}}
		result, err := h.GetInboundUserCount(ctx, &userv1.GetInboundUserCountRequest{InstanceId: " edge ", InboundTag: " exact>>>tag "})
		if err != nil || calls != 1 || result.InstanceId != " edge " || result.InboundTag != " exact>>>tag " || result.Count != count || result.ObservedAtUnixMs != observed.UnixMilli() {
			t.Fatalf("wire count changed: %v %v", result, err)
		}
	}
}

func TestUserCountRejectsInvalidSelectionBeforeApplication(t *testing.T) {
	h := grpcapi.Handler{UserAPI: usergrpc.UserAPI{ObservationAPI: observations.ObservationAPI{AllowUsersRead: true}}}
	for _, request := range []*userv1.GetInboundUserCountRequest{nil, {}, {InstanceId: "edge", InboundTag: " "}, {InstanceId: " ", InboundTag: "in"}, {InstanceId: "edge", InboundTag: "\xff"}} {
		if _, err := h.GetInboundUserCount(testutil.VerifiedGRPCContext(t), request); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("invalid selection reached application: %v", err)
		}
	}
}

func TestUserCountSanitizesFailureAndNeverReturnsSuccessfulZero(t *testing.T) {
	for _, tc := range []struct {
		cause error
		code  codes.Code
	}{
		{instances.ErrNotFound, codes.NotFound}, {domain.ErrInboundNotFound, codes.NotFound},
		{domain.ErrAPIDisabled, codes.FailedPrecondition}, {domain.ErrUserReadUnsupported, codes.FailedPrecondition},
		{domain.ErrAPIBusy, codes.ResourceExhausted}, {domain.ErrAPIResourceLimit, codes.ResourceExhausted},
		{domain.ErrInvalidUserCount, codes.Unavailable}, {context.Canceled, codes.Canceled}, {context.DeadlineExceeded, codes.DeadlineExceeded},
	} {
		h := grpcapi.Handler{UserAPI: usergrpc.UserAPI{ObservationAPI: observations.ObservationAPI{AllowUsersRead: true, UserCounts: inboundUserCounter(func(context.Context, instances.InstanceID, string) (domain.InstanceInboundUserCount, error) {
			return domain.InstanceInboundUserCount{}, errors.Join(tc.cause, errors.New("private endpoint/credentials"))
		})}}}
		response, err := h.GetInboundUserCount(testutil.VerifiedGRPCContext(t), &userv1.GetInboundUserCountRequest{InstanceId: "edge", InboundTag: "in"})
		if response != nil || status.Code(err) != tc.code || strings.Contains(err.Error(), "private") {
			t.Fatalf("failed count became successful facts or disclosed diagnostics: %v %v", response, err)
		}
	}
}
