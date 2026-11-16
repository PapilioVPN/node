package grpcapi_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	usergrpc "github.com/PapilioVPN/node/internal/modules/core/users/grpcapi"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/grpcapi"
	xrayv1 "github.com/PapilioVPN/node/internal/modules/engines/xray/grpcapi/proto/v1"
)

type balancerOverrider func(context.Context, instances.InstanceID, domain.BalancerOverride) (domain.InstanceBalancerOverride, error)

func (f balancerOverrider) OverrideBalancerTarget(ctx context.Context, id instances.InstanceID, selection domain.BalancerOverride) (domain.InstanceBalancerOverride, error) {
	return f(ctx, id, selection)
}

func requireRoutingMutationOutcome(t *testing.T, err error, want xrayv1.RoutingMutationOutcome) {
	t.Helper()
	details := status.Convert(err).Details()
	if len(details) != 1 {
		t.Fatalf("routing mutation outcome detail missing: %v", err)
	}
	failure, ok := details[0].(*xrayv1.RoutingMutationFailure)
	if !ok || failure.Outcome != want {
		t.Fatalf("routing mutation effects misreported: %v want %v", details, want)
	}
}

func TestBalancerOverrideRequiresVerifiedIdentityBeforeShape(t *testing.T) {
	_, err := (grpcapi.Handler{}).OverrideBalancerTarget(t.Context(), nil)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("unverified override reached shape/application: %v", err)
	}
	requireRoutingMutationOutcome(t, err, xrayv1.RoutingMutationOutcome_ROUTING_MUTATION_OUTCOME_NOT_APPLIED)
	h := grpcapi.Handler{AllowInfoRead: true, AllowStatsRead: true, AllowStatsReset: true, AllowOnlineRead: true, AllowRoutingRead: true, AllowRoutingTest: true, UserAPI: usergrpc.UserAPI{AllowUsersRead: true, AllowUserManagement: true}}
	_, err = h.OverrideBalancerTarget(verifiedContext(t), nil)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("other grants authorized a routing mutation: %v", err)
	}
	requireRoutingMutationOutcome(t, err, xrayv1.RoutingMutationOutcome_ROUTING_MUTATION_OUTCOME_NOT_APPLIED)
}

func TestBalancerOverrideRequiresExplicitTargetAndValidShapeBeforeApplication(t *testing.T) {
	h := grpcapi.Handler{AllowRoutingManagement: true}
	for _, request := range []*xrayv1.OverrideBalancerTargetRequest{
		nil, {}, {InstanceId: " "}, {InstanceId: "edge", BalancerTag: "pool"},
		{InstanceId: "edge", Target: proto.String("")},
		{InstanceId: "edge", BalancerTag: " ", Target: proto.String("target")},
		{InstanceId: "edge", BalancerTag: "\xff", Target: proto.String("target")},
		{InstanceId: "edge", BalancerTag: "pool", Target: proto.String("\xff")},
	} {
		response, err := h.OverrideBalancerTarget(verifiedContext(t), request)
		if response != nil || status.Code(err) != codes.InvalidArgument {
			t.Fatalf("invalid/absent mutation input reached application: %v %v", response, err)
		}
		requireRoutingMutationOutcome(t, err, xrayv1.RoutingMutationOutcome_ROUTING_MUTATION_OUTCOME_NOT_APPLIED)
	}
}

func TestBalancerOverridePreservesOpaqueSelectionClearAndLateAcknowledgement(t *testing.T) {
	for _, target := range []string{"", "   ", " missing>>>outside "} {
		ctx, cancel := context.WithCancel(verifiedContext(t))
		defer cancel()
		calls, ack := 0, time.UnixMilli(1700000000123)
		selection := domain.BalancerOverride{BalancerTag: " pool ", Target: target}
		h := grpcapi.Handler{AllowRoutingManagement: true, BalancerOverrides: balancerOverrider(func(received context.Context, id instances.InstanceID, value domain.BalancerOverride) (domain.InstanceBalancerOverride, error) {
			calls++
			if received != ctx || id != " edge " || value != selection {
				t.Fatal("mutation context/identity/target changed")
			}
			cancel()
			return domain.InstanceBalancerOverride{InstanceID: id, Override: value, Acknowledgement: domain.RoutingMutationAcknowledgement{AcknowledgedAt: ack}}, nil
		})}
		response, err := h.OverrideBalancerTarget(ctx, &xrayv1.OverrideBalancerTargetRequest{InstanceId: " edge ", BalancerTag: " pool ", Target: proto.String(target)})
		if err != nil || calls != 1 || response.InstanceId != " edge " || response.BalancerTag != " pool " || response.Target != target || response.AcknowledgedAtUnixMs != ack.UnixMilli() {
			t.Fatalf("confirmed assignment changed/disappeared: %v %v", response, err)
		}
	}
}

func TestBalancerOverrideFailureOutcomesAndPrivateDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		cause   error
		code    codes.Code
		unknown bool
	}{
		{instances.ErrNotFound, codes.NotFound, false}, {domain.ErrAPIDisabled, codes.FailedPrecondition, false},
		{domain.ErrRoutingManagementUnsupported, codes.FailedPrecondition, false}, {domain.ErrInvalidBalancerOverride, codes.InvalidArgument, false},
		{domain.ErrAPIBusy, codes.ResourceExhausted, false}, {grpc.ErrClientConnClosing, codes.Unavailable, false},
		{context.Canceled, codes.Canceled, false}, {context.DeadlineExceeded, codes.DeadlineExceeded, false},
		{domain.ErrRoutingMutationUnknown, codes.Unavailable, true}, {domain.ErrAPIResourceLimit, codes.ResourceExhausted, true},
		{errors.New("unclassified adapter failure"), codes.Unavailable, true},
		{errors.Join(domain.ErrRoutingMutationUnknown, context.Canceled), codes.Canceled, true},
		{errors.Join(domain.ErrRoutingMutationUnknown, context.DeadlineExceeded), codes.DeadlineExceeded, true},
		{errors.Join(domain.ErrRoutingMutationUnknown, domain.ErrAPIResourceLimit), codes.ResourceExhausted, true},
		{errors.Join(domain.ErrRoutingMutationUnknown, instances.ErrNotFound), codes.NotFound, true},
		{errors.Join(domain.ErrRoutingMutationUnknown, domain.ErrRoutingManagementUnsupported), codes.FailedPrecondition, true},
	} {
		h := grpcapi.Handler{AllowRoutingManagement: true, BalancerOverrides: balancerOverrider(func(context.Context, instances.InstanceID, domain.BalancerOverride) (domain.InstanceBalancerOverride, error) {
			return domain.InstanceBalancerOverride{Acknowledgement: domain.RoutingMutationAcknowledgement{AcknowledgedAt: time.Now()}}, errors.Join(tc.cause, errors.New("private endpoint/config"))
		})}
		response, err := h.OverrideBalancerTarget(verifiedContext(t), &xrayv1.OverrideBalancerTargetRequest{InstanceId: "edge", BalancerTag: "pool", Target: proto.String("")})
		if response != nil || status.Code(err) != tc.code || strings.Contains(err.Error(), "private") {
			t.Fatalf("failed change exposed diagnostics/partial success: %v %v", response, err)
		}
		want := xrayv1.RoutingMutationOutcome_ROUTING_MUTATION_OUTCOME_NOT_APPLIED
		if tc.unknown {
			want = xrayv1.RoutingMutationOutcome_ROUTING_MUTATION_OUTCOME_UNKNOWN
		}
		requireRoutingMutationOutcome(t, err, want)
	}
}
