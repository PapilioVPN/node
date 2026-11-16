package routing_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"testing"
	"time"

	"go.uber.org/fx"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/modules/xray/routing"
	"github.com/PapilioVPN/node/internal/modules/xray/routing/domain"
	"github.com/PapilioVPN/node/internal/modules/xray/routing/grpcapi"
	routingv1 "github.com/PapilioVPN/node/internal/modules/xray/routing/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/settings"
)

type reader struct{ calls int }

func (r *reader) ReadRoutingRules(context.Context) (domain.RoutingRules, error) {
	r.calls++
	return domain.RoutingRules{Items: []domain.RoutingRuleReference{{RuleTag: " second ", OutboundTag: ""}, {RuleTag: "first", OutboundTag: "opaque>>>tag"}}, ObservedAt: time.UnixMilli(1700000000123)}, nil
}

func (*reader) ReadBalancerInfo(context.Context, string) (domain.BalancerInfo, error) {
	panic("unrequested balancer read")
}

type bindings struct {
	fx.Out
	Values []routing.BackendBinding `group:"routing_backends,flatten"`
}

func TestRoutingModuleConsumesFocusedPortsWithoutAnEngineModule(t *testing.T) {
	backend := &reader{}
	var api grpcapi.RoutingAPI
	app := fx.New(fx.NopLogger, fx.Supply(settings.GRPCPermissions{ReadEngineRouting: true}),
		fx.Provide(func() bindings {
			return bindings{Values: []routing.BackendBinding{{InstanceID: " edge ", Reader: backend}, {InstanceID: "disabled"}}}
		}), routing.Module(true), fx.Populate(&api))
	if err := app.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := app.Stop(context.Background()); err != nil {
			t.Error(err)
		}
	})
	// Controlled unit peer; bootstrap/native tests retain real mTLS verification.
	ctx := peer.NewContext(t.Context(), &peer.Peer{AuthInfo: credentials.TLSInfo{State: tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{}}}}}})
	response, err := api.ListRoutingRules(ctx, &routingv1.ListRoutingRulesRequest{InstanceId: " edge "})
	if err != nil || response.InstanceId != " edge " || len(response.Rules) != 2 || response.Rules[0].RuleTag != " second " || response.Rules[0].OutboundTag != "" || response.Rules[1].RuleTag != "first" || response.Rules[1].OutboundTag != "opaque>>>tag" || response.ObservedAtUnixMs != 1700000000123 || backend.calls != 1 {
		t.Fatalf("module lost selection/order/facts or introduced another read: %v", err)
	}
	for id, want := range map[string]codes.Code{"missing": codes.NotFound, "disabled": codes.FailedPrecondition} {
		response, err := api.ListRoutingRules(ctx, &routingv1.ListRoutingRulesRequest{InstanceId: id})
		if response != nil || status.Code(err) != want || backend.calls != 1 {
			t.Fatalf("module lost absent/disabled ownership: %v", err)
		}
	}
	if _, err := api.TestRoute(ctx, &routingv1.TestRouteRequest{InstanceId: " edge ", Context: &routingv1.RoutingContext{}}); status.Code(err) != codes.PermissionDenied {
		t.Fatal("read ports enabled probes")
	}
	if _, err := api.RemoveRoutingRule(ctx, &routingv1.RemoveRoutingRuleRequest{InstanceId: " edge ", RuleTag: "rule"}); status.Code(err) != codes.PermissionDenied {
		t.Fatal("read ports enabled mutations")
	}
}

func TestRoutingModuleRejectsAmbiguousBackendOwnership(t *testing.T) {
	var api grpcapi.RoutingAPI
	app := fx.New(fx.NopLogger, fx.Supply(settings.GRPCPermissions{}),
		fx.Provide(func() bindings {
			return bindings{Values: []routing.BackendBinding{{InstanceID: "same"}, {InstanceID: "same"}}}
		}),
		routing.Module(true), fx.Populate(&api))
	if err := app.Err(); err == nil {
		t.Fatal("duplicate routing backend ownership was silently selected")
	}
}
