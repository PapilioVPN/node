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
	"github.com/PapilioVPN/node/internal/modules/core/routing/domain"
	routinggrpc "github.com/PapilioVPN/node/internal/modules/core/routing/grpcapi"
	routingv1 "github.com/PapilioVPN/node/internal/modules/core/routing/grpcapi/proto/v1"
	trafficgrpc "github.com/PapilioVPN/node/internal/modules/core/traffic/grpcapi"
	usergrpc "github.com/PapilioVPN/node/internal/modules/core/users/grpcapi"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/grpcapi"
)

type routingRuleRemoval func(context.Context, instances.InstanceID, string) (domain.InstanceRoutingRuleRemoval, error)

func (f routingRuleRemoval) RemoveRoutingRule(ctx context.Context, id instances.InstanceID, tag string) (domain.InstanceRoutingRuleRemoval, error) {
	return f(ctx, id, tag)
}

func TestRuleRemovalRequiresIdentityBeforeShape(t *testing.T) {
	_, err := (grpcapi.Handler{}).RemoveRoutingRule(t.Context(), nil)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("unverified removal reached shape/application: %v", err)
	}
	requireRoutingMutationOutcome(t, err, routingv1.RoutingMutationOutcome_ROUTING_MUTATION_OUTCOME_NOT_APPLIED)
	h := grpcapi.Handler{AllowInfoRead: true, AllowOnlineRead: true, UserAPI: usergrpc.UserAPI{AllowUsersRead: true, AllowUserManagement: true}, RoutingAPI: routinggrpc.RoutingAPI{AllowRoutingRead: true, AllowRoutingTest: true}, TrafficAPI: trafficgrpc.TrafficAPI{AllowStatsRead: true, AllowStatsReset: true}}
	_, err = h.RemoveRoutingRule(verifiedContext(t), nil)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("other grants authorized rule removal: %v", err)
	}
	requireRoutingMutationOutcome(t, err, routingv1.RoutingMutationOutcome_ROUTING_MUTATION_OUTCOME_NOT_APPLIED)
}

func TestRuleRemovalRejectsInvalidShapeBeforeApplication(t *testing.T) {
	h := grpcapi.Handler{RoutingAPI: routinggrpc.RoutingAPI{AllowRoutingManagement: true}}
	for _, request := range []*routingv1.RemoveRoutingRuleRequest{nil, {}, {InstanceId: " "}, {InstanceId: "edge"}, {InstanceId: "edge", RuleTag: "\xff"}} {
		response, err := h.RemoveRoutingRule(verifiedContext(t), request)
		if response != nil || status.Code(err) != codes.InvalidArgument {
			t.Fatalf("invalid removal reached application: %v %v", response, err)
		}
		requireRoutingMutationOutcome(t, err, routingv1.RoutingMutationOutcome_ROUTING_MUTATION_OUTCOME_NOT_APPLIED)
	}
}

func TestRuleRemovalPreservesOpaqueTagsAndLateAcknowledgement(t *testing.T) {
	for _, tag := range []string{" exact>>>rule ", "   ", "missing"} {
		ctx, cancel := context.WithCancel(verifiedContext(t))
		defer cancel()
		calls, ack := 0, time.UnixMilli(1700000000123)
		h := grpcapi.Handler{RoutingAPI: routinggrpc.RoutingAPI{AllowRoutingManagement: true, RuleRemoval: routingRuleRemoval(func(received context.Context, id instances.InstanceID, value string) (domain.InstanceRoutingRuleRemoval, error) {
			calls++
			if received != ctx || id != " edge " || value != tag {
				t.Fatal("removal context/identity/tag changed")
			}
			cancel()
			return domain.InstanceRoutingRuleRemoval{InstanceID: id, RuleTag: value, Acknowledgement: domain.RoutingMutationAcknowledgement{AcknowledgedAt: ack}}, nil
		})}}
		response, err := h.RemoveRoutingRule(ctx, &routingv1.RemoveRoutingRuleRequest{InstanceId: " edge ", RuleTag: tag})
		if err != nil || calls != 1 || response.InstanceId != " edge " || response.RuleTag != tag || response.AcknowledgedAtUnixMs != ack.UnixMilli() {
			t.Fatalf("confirmed removal disappeared/changed: %v %v", response, err)
		}
	}
}

func TestRuleRemovalRetainsKnownAndUnknownOutcomesWithoutDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		cause   error
		code    codes.Code
		unknown bool
	}{
		{instances.ErrNotFound, codes.NotFound, false}, {domain.ErrAPIDisabled, codes.FailedPrecondition, false},
		{domain.ErrRoutingManagementUnsupported, codes.FailedPrecondition, false}, {domain.ErrInvalidRoutingRuleTag, codes.InvalidArgument, false},
		{domain.ErrAPIBusy, codes.ResourceExhausted, false}, {context.Canceled, codes.Canceled, false},
		{errors.Join(domain.ErrRoutingMutationUnknown, context.Canceled), codes.Canceled, true},
		{errors.Join(domain.ErrRoutingMutationUnknown, context.DeadlineExceeded), codes.DeadlineExceeded, true},
		{domain.ErrAPIResourceLimit, codes.ResourceExhausted, true}, {errors.New("unexpected adapter failure"), codes.Unavailable, true},
		{errors.Join(domain.ErrRoutingMutationUnknown, instances.ErrNotFound), codes.NotFound, true},
	} {
		h := grpcapi.Handler{RoutingAPI: routinggrpc.RoutingAPI{AllowRoutingManagement: true, RuleRemoval: routingRuleRemoval(func(context.Context, instances.InstanceID, string) (domain.InstanceRoutingRuleRemoval, error) {
			return domain.InstanceRoutingRuleRemoval{RuleTag: "partial", Acknowledgement: domain.RoutingMutationAcknowledgement{AcknowledgedAt: time.Now()}}, errors.Join(tc.cause, errors.New("private config/path"))
		})}}
		response, err := h.RemoveRoutingRule(verifiedContext(t), &routingv1.RemoveRoutingRuleRequest{InstanceId: "edge", RuleTag: "rule"})
		if response != nil || status.Code(err) != tc.code || strings.Contains(err.Error(), "private") {
			t.Fatalf("failed removal leaked/returned success: %v %v", response, err)
		}
		want := routingv1.RoutingMutationOutcome_ROUTING_MUTATION_OUTCOME_NOT_APPLIED
		if tc.unknown {
			want = routingv1.RoutingMutationOutcome_ROUTING_MUTATION_OUTCOME_UNKNOWN
		}
		requireRoutingMutationOutcome(t, err, want)
	}
}
