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
	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/grpcapi"
	xrayv1 "github.com/PapilioVPN/node/internal/modules/engines/xray/grpcapi/proto/v1"
)

type routingRuleAdder func(context.Context, instances.InstanceID, domain.RoutingRuleAddition) (domain.InstanceRoutingRuleAddition, error)

func (f routingRuleAdder) AddRoutingRule(ctx context.Context, id instances.InstanceID, input domain.RoutingRuleAddition) (domain.InstanceRoutingRuleAddition, error) {
	return f(ctx, id, input)
}

func TestRuleAdditionRequiresIdentityBeforeShape(t *testing.T) {
	_, err := (grpcapi.Handler{}).AddRoutingRule(t.Context(), nil)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("unverified addition reached shape/application: %v", err)
	}
	requireRoutingMutationOutcome(t, err, xrayv1.RoutingMutationOutcome_ROUTING_MUTATION_OUTCOME_NOT_APPLIED)
	h := grpcapi.Handler{AllowInfoRead: true, AllowStatsRead: true, AllowStatsReset: true, AllowOnlineRead: true, AllowUsersRead: true, AllowUserManagement: true, AllowRoutingRead: true, AllowRoutingTest: true}
	_, err = h.AddRoutingRule(verifiedContext(t), nil)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("other grants enabled rule addition: %v", err)
	}
	requireRoutingMutationOutcome(t, err, xrayv1.RoutingMutationOutcome_ROUTING_MUTATION_OUTCOME_NOT_APPLIED)
}

func TestRuleAdditionRequiresExplicitModeAndShapeBeforeApplication(t *testing.T) {
	h := grpcapi.Handler{AllowRoutingManagement: true}
	for _, request := range []*xrayv1.AddRoutingRuleRequest{nil, {}, {InstanceId: " "}, {InstanceId: "edge", Mode: xrayv1.RoutingRuleMode_ROUTING_RULE_MODE_APPEND}} {
		response, err := h.AddRoutingRule(verifiedContext(t), request)
		if response != nil || status.Code(err) != codes.InvalidArgument {
			t.Fatalf("invalid rule shape reached application: %v %v", response, err)
		}
		requireRoutingMutationOutcome(t, err, xrayv1.RoutingMutationOutcome_ROUTING_MUTATION_OUTCOME_NOT_APPLIED)
	}
	for _, mode := range []xrayv1.RoutingRuleMode{0, -1, 255, 257, 258} {
		response, err := h.AddRoutingRule(verifiedContext(t), &xrayv1.AddRoutingRuleRequest{InstanceId: "edge", RuleJson: []byte("{}"), Mode: mode})
		if response != nil || status.Code(err) != codes.InvalidArgument {
			t.Fatalf("mode wrapped/defaulted into destructive replacement: %v %v", response, err)
		}
		requireRoutingMutationOutcome(t, err, xrayv1.RoutingMutationOutcome_ROUTING_MUTATION_OUTCOME_NOT_APPLIED)
	}
}

func TestRuleAdditionForwardsNativeJSONAndPreservesAnonymousLateAcknowledgement(t *testing.T) {
	for _, mode := range []struct {
		wire   xrayv1.RoutingRuleMode
		domain domain.RoutingRuleMode
	}{{xrayv1.RoutingRuleMode_ROUTING_RULE_MODE_APPEND, domain.AppendRoutingRule}, {xrayv1.RoutingRuleMode_ROUTING_RULE_MODE_REPLACE, domain.ReplaceRoutingRules}} {
		for _, tag := range []string{"", "   ", " exact>>>rule "} {
			ctx, cancel := context.WithCancel(verifiedContext(t))
			defer cancel()
			input := []byte(`{"native-field":"opaque","webhook":{"headers":{"private":"secret"}}}`)
			calls, ack := 0, time.UnixMilli(1700000000123)
			h := grpcapi.Handler{AllowRoutingManagement: true, RuleAddition: routingRuleAdder(func(received context.Context, id instances.InstanceID, addition domain.RoutingRuleAddition) (domain.InstanceRoutingRuleAddition, error) {
				calls++
				if received != ctx || id != " edge " || addition.Mode != mode.domain || &addition.JSON[0] != &input[0] {
					t.Fatal("native input/identity/mode/context changed")
				}
				cancel()
				return domain.InstanceRoutingRuleAddition{InstanceID: id, RuleTag: tag, Mode: addition.Mode, Acknowledgement: domain.RoutingMutationAcknowledgement{AcknowledgedAt: ack}}, nil
			})}
			response, err := h.AddRoutingRule(ctx, &xrayv1.AddRoutingRuleRequest{InstanceId: " edge ", RuleJson: input, Mode: mode.wire})
			if err != nil || calls != 1 || response.InstanceId != " edge " || response.RuleTag != tag || response.Mode != mode.wire || response.AcknowledgedAtUnixMs != ack.UnixMilli() || strings.Contains(response.String(), "secret") {
				t.Fatalf("native acknowledgement changed/lost/exposed JSON: %v %v", response, err)
			}
		}
	}
}

func TestRuleAdditionFailureDistinguishesPreparationAndUncertainApplication(t *testing.T) {
	for _, tc := range []struct {
		cause   error
		code    codes.Code
		unknown bool
	}{
		{instances.ErrNotFound, codes.NotFound, false}, {domain.ErrAPIDisabled, codes.FailedPrecondition, false},
		{domain.ErrRoutingRulePreparationDisabled, codes.FailedPrecondition, false}, {domain.ErrRoutingManagementUnsupported, codes.FailedPrecondition, false},
		{domain.ErrInvalidRoutingRuleMode, codes.InvalidArgument, false}, {errors.Join(domain.ErrRoutingRulePreparationFailed, domain.ErrInvalidRoutingRule), codes.InvalidArgument, false},
		{errors.Join(domain.ErrRoutingRulePreparationFailed, domain.ErrInvalidPreparedRoutingRule), codes.FailedPrecondition, false},
		{domain.ErrRoutingRulePreparationBusy, codes.ResourceExhausted, false}, {domain.ErrRoutingRuleInputLimit, codes.ResourceExhausted, false}, {domain.ErrRoutingRuleOutputLimit, codes.ResourceExhausted, false},
		{domain.ErrRoutingRulePreparationFailed, codes.Unavailable, false}, {errors.Join(domain.ErrRoutingRulePreparationFailed, context.Canceled), codes.Canceled, false},
		{errors.Join(domain.ErrRoutingRulePreparationFailed, context.DeadlineExceeded), codes.DeadlineExceeded, false},
		{domain.ErrAPIBusy, codes.ResourceExhausted, false}, {domain.ErrAPIResourceLimit, codes.ResourceExhausted, true},
		{errors.New("unclassified application failure"), codes.Unavailable, true}, {errors.Join(domain.ErrRoutingMutationUnknown, context.Canceled), codes.Canceled, true},
		{errors.Join(domain.ErrRoutingMutationUnknown, context.DeadlineExceeded), codes.DeadlineExceeded, true},
		{errors.Join(domain.ErrRoutingMutationUnknown, domain.ErrInvalidRoutingRule), codes.InvalidArgument, true},
		{errors.Join(domain.ErrRoutingMutationUnknown, domain.ErrRoutingRulePreparationFailed), codes.Unavailable, true},
	} {
		h := grpcapi.Handler{AllowRoutingManagement: true, RuleAddition: routingRuleAdder(func(context.Context, instances.InstanceID, domain.RoutingRuleAddition) (domain.InstanceRoutingRuleAddition, error) {
			return domain.InstanceRoutingRuleAddition{RuleTag: "partial", Acknowledgement: domain.RoutingMutationAcknowledgement{AcknowledgedAt: time.Now()}}, errors.Join(tc.cause, errors.New("private path/config/headers"))
		})}
		response, err := h.AddRoutingRule(verifiedContext(t), &xrayv1.AddRoutingRuleRequest{InstanceId: "edge", RuleJson: []byte("{}"), Mode: xrayv1.RoutingRuleMode_ROUTING_RULE_MODE_APPEND})
		if response != nil || status.Code(err) != tc.code || strings.Contains(err.Error(), "private") {
			t.Fatalf("failed addition leaked/returned successful partial facts: %v %v", response, err)
		}
		want := xrayv1.RoutingMutationOutcome_ROUTING_MUTATION_OUTCOME_NOT_APPLIED
		if tc.unknown {
			want = xrayv1.RoutingMutationOutcome_ROUTING_MUTATION_OUTCOME_UNKNOWN
		}
		requireRoutingMutationOutcome(t, err, want)
	}
}
