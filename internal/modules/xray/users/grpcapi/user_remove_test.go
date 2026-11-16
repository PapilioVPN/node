package grpcapi_test

import (
	"context"
	"errors"
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

type userRemover func(context.Context, instances.InstanceID, domain.InboundUserSelection) (domain.InstanceInboundUserMutation, error)

func (f userRemover) RemoveInboundUser(ctx context.Context, id instances.InstanceID, selection domain.InboundUserSelection) (domain.InstanceInboundUserMutation, error) {
	return f(ctx, id, selection)
}

func TestUserRemovalChecksIdentityAndIndependentGrantBeforeShape(t *testing.T) {
	if _, err := (grpcapi.Handler{UserAPI: usergrpc.UserAPI{AllowUserManagement: true}}).RemoveInboundUser(t.Context(), nil); status.Code(err) != codes.Unauthenticated {
		t.Fatal(err)
	}
	if _, err := (grpcapi.Handler{TrafficAPI: trafficgrpc.TrafficAPI{AllowStatsRead: true, AllowStatsReset: true}, InfoAPI: infogrpc.InfoAPI{AllowInfoRead: true}, UserAPI: usergrpc.UserAPI{AllowOnlineRead: true}}).RemoveInboundUser(verifiedContext(t), nil); status.Code(err) != codes.PermissionDenied {
		t.Fatal(err)
	}
	h := grpcapi.Handler{UserAPI: usergrpc.UserAPI{AllowUserManagement: true}}
	for _, r := range []*userv1.RemoveInboundUserRequest{nil, {}, {InstanceId: "edge", InboundTag: " ", Email: "user"}, {InstanceId: "edge", InboundTag: "in", Email: " "}, {InstanceId: "edge", InboundTag: "in", Email: "\xff"}} {
		if _, err := h.RemoveInboundUser(verifiedContext(t), r); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("invalid selection reached application: %v", err)
		}
	}
}

func TestUserRemovalPreservesIdentityAndConfirmedLateAcknowledgement(t *testing.T) {
	ctx, cancel := context.WithCancel(verifiedContext(t))
	defer cancel()
	ack := time.UnixMilli(1700000000123)
	h := grpcapi.Handler{UserAPI: usergrpc.UserAPI{AllowUserManagement: true, UserRemoval: userRemover(func(received context.Context, id instances.InstanceID, selection domain.InboundUserSelection) (domain.InstanceInboundUserMutation, error) {
		if received != ctx || id != " edge " || selection != (domain.InboundUserSelection{InboundTag: " exact>>>tag ", Email: " Mixed@Example "}) {
			t.Fatal("identity/context changed")
		}
		cancel()
		return domain.InstanceInboundUserMutation{InstanceID: id, Selection: selection, Acknowledgement: domain.UserMutationAcknowledgement{AcknowledgedAt: ack}}, nil
	})}}
	response, err := h.RemoveInboundUser(ctx, &userv1.RemoveInboundUserRequest{InstanceId: " edge ", InboundTag: " exact>>>tag ", Email: " Mixed@Example "})
	if err != nil || response.InstanceId != " edge " || response.InboundTag != " exact>>>tag " || response.Email != " Mixed@Example " || response.AcknowledgedAtUnixMs != ack.UnixMilli() {
		t.Fatalf("acknowledgement lost: %v %v", response, err)
	}
}

func TestUserRemovalFailurePreservesUnknownOutcomeAndSanitizesDiagnostics(t *testing.T) {
	for _, test := range []struct {
		cause   error
		code    codes.Code
		unknown bool
	}{
		{instances.ErrNotFound, codes.NotFound, false}, {domain.ErrAPIDisabled, codes.FailedPrecondition, false}, {domain.ErrAPIBusy, codes.ResourceExhausted, false}, {domain.ErrInvalidUserSelection, codes.InvalidArgument, false}, {domain.ErrUserManagementUnsupported, codes.FailedPrecondition, false}, {context.Canceled, codes.Canceled, false},
		{domain.ErrUserMutationUnknown, codes.Unavailable, true}, {errors.Join(domain.ErrUserMutationUnknown, domain.ErrAPIResourceLimit), codes.ResourceExhausted, true}, {errors.Join(domain.ErrUserMutationUnknown, context.DeadlineExceeded), codes.DeadlineExceeded, true},
	} {
		h := grpcapi.Handler{UserAPI: usergrpc.UserAPI{AllowUserManagement: true, UserRemoval: userRemover(func(context.Context, instances.InstanceID, domain.InboundUserSelection) (domain.InstanceInboundUserMutation, error) {
			return domain.InstanceInboundUserMutation{Acknowledgement: domain.UserMutationAcknowledgement{AcknowledgedAt: time.Now()}}, errors.Join(test.cause, errors.New("private email/config/endpoint"))
		})}}
		response, err := h.RemoveInboundUser(verifiedContext(t), &userv1.RemoveInboundUserRequest{InstanceId: "edge", InboundTag: "in", Email: "user"})
		if response != nil || status.Code(err) != test.code || strings.Contains(err.Error(), "private") {
			t.Fatalf("failure: %v %v", response, err)
		}
		details := status.Convert(err).Details()
		if len(details) != 1 {
			t.Fatalf("effect detail missing: %v", err)
		}
		failure, ok := details[0].(*userv1.UserMutationFailure)
		want := userv1.UserMutationOutcome_USER_MUTATION_OUTCOME_NOT_APPLIED
		if test.unknown {
			want = userv1.UserMutationOutcome_USER_MUTATION_OUTCOME_UNKNOWN
		}
		if !ok || failure.Outcome != want {
			t.Fatalf("incorrect effect claim: %v", details)
		}
	}
}
