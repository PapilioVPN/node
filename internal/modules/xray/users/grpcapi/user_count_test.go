package grpcapi_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/xray/grpcapi"
	infogrpc "github.com/PapilioVPN/node/internal/modules/xray/info/grpcapi"
	trafficgrpc "github.com/PapilioVPN/node/internal/modules/xray/traffic/grpcapi"
	"github.com/PapilioVPN/node/internal/modules/xray/users/domain"
	usergrpc "github.com/PapilioVPN/node/internal/modules/xray/users/grpcapi"
	userv1 "github.com/PapilioVPN/node/internal/modules/xray/users/grpcapi/proto/v1"
)

type inboundUserCounter func(context.Context, instances.InstanceID, string) (domain.InstanceInboundUserCount, error)

func (f inboundUserCounter) GetInboundUserCount(ctx context.Context, id instances.InstanceID, tag string) (domain.InstanceInboundUserCount, error) {
	return f(ctx, id, tag)
}

func TestUserCountRequiresIdentityAndItsOwnGrantBeforeShape(t *testing.T) {
	if _, err := (grpcapi.Handler{}).GetInboundUserCount(t.Context(), nil); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("unverified count request reached shape/application: %v", err)
	}
	if _, err := (grpcapi.Handler{AllowOnlineRead: true, UserAPI: usergrpc.UserAPI{AllowUserManagement: true}, TrafficAPI: trafficgrpc.TrafficAPI{AllowStatsRead: true, AllowStatsReset: true}, InfoAPI: infogrpc.InfoAPI{AllowInfoRead: true}}).GetInboundUserCount(verifiedContext(t), nil); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("other grants enabled user counts: %v", err)
	}
}

func TestUserCountPreservesOpaqueIdentityZeroAndFullNativePrecision(t *testing.T) {
	for _, count := range []int64{0, math.MaxInt64} {
		ctx := verifiedContext(t)
		observed := time.UnixMilli(1700000000123)
		calls := 0
		h := grpcapi.Handler{UserAPI: usergrpc.UserAPI{AllowUsersRead: true, UserCounts: inboundUserCounter(func(received context.Context, id instances.InstanceID, tag string) (domain.InstanceInboundUserCount, error) {
			calls++
			if received != ctx || id != " edge " || tag != " exact>>>tag " {
				t.Fatal("count selection/context changed")
			}
			return domain.InstanceInboundUserCount{InstanceID: id, InboundTag: tag, Users: domain.InboundUserCount{Count: count, ObservedAt: observed}}, nil
		})}}
		result, err := h.GetInboundUserCount(ctx, &userv1.GetInboundUserCountRequest{InstanceId: " edge ", InboundTag: " exact>>>tag "})
		if err != nil || calls != 1 || result.InstanceId != " edge " || result.InboundTag != " exact>>>tag " || result.Count != count || result.ObservedAtUnixMs != observed.UnixMilli() {
			t.Fatalf("wire count changed: %v %v", result, err)
		}
	}
}

func TestUserCountRejectsInvalidSelectionBeforeApplication(t *testing.T) {
	h := grpcapi.Handler{UserAPI: usergrpc.UserAPI{AllowUsersRead: true}}
	for _, request := range []*userv1.GetInboundUserCountRequest{nil, {}, {InstanceId: "edge", InboundTag: " "}, {InstanceId: " ", InboundTag: "in"}, {InstanceId: "edge", InboundTag: "\xff"}} {
		if _, err := h.GetInboundUserCount(verifiedContext(t), request); status.Code(err) != codes.InvalidArgument {
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
		h := grpcapi.Handler{UserAPI: usergrpc.UserAPI{AllowUsersRead: true, UserCounts: inboundUserCounter(func(context.Context, instances.InstanceID, string) (domain.InstanceInboundUserCount, error) {
			return domain.InstanceInboundUserCount{}, errors.Join(tc.cause, errors.New("private endpoint/credentials"))
		})}}
		response, err := h.GetInboundUserCount(verifiedContext(t), &userv1.GetInboundUserCountRequest{InstanceId: "edge", InboundTag: "in"})
		if response != nil || status.Code(err) != tc.code || strings.Contains(err.Error(), "private") {
			t.Fatalf("failed count became successful facts or disclosed diagnostics: %v %v", response, err)
		}
	}
}
