package grpcapi

import (
	"context"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/app/services"
	v1 "github.com/PapilioVPN/node/internal/apps/xray/network/routing/grpcapi/proto/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestRoutingInputPresenceAndWidthsBeforeInstanceLookup(t *testing.T) {
	service, err := services.New(nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	h := Handler{Service: service, Timeout: time.Second}
	ctx := context.Background()
	for _, r := range []*v1.AddRoutingRuleRequest{nil, {InstanceId: "unknown", RuleJson: []byte(`{}`)}, {InstanceId: "unknown", RuleJson: []byte(`{}`), Mode: v1.RuleMode(257)}, {InstanceId: "unknown", RuleJson: []byte(`{}`), Mode: v1.RuleMode_RULE_MODE_REPLACE}} {
		_, err := h.AddRoutingRule(ctx, r)
		if status.Code(err) != codes.InvalidArgument {
			t.Fatal(r, err)
		}
		if len(status.Convert(err).Details()) != 1 {
			t.Fatal("missing known-not-applied evidence", err)
		}
		if r != nil {
			detail := status.Convert(err).Details()[0].(*v1.RoutingOperation)
			if detail.InstanceId != r.InstanceId {
				t.Fatal("instance selection lost", detail)
			}
		}
	}
	_, err = h.OverrideBalancerTarget(ctx, &v1.OverrideBalancerTargetRequest{InstanceId: "unknown", BalancerTag: "b"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
	clear := ""
	_, err = h.OverrideBalancerTarget(ctx, &v1.OverrideBalancerTargetRequest{InstanceId: "unknown", BalancerTag: "b", Target: &clear})
	if status.Code(err) != codes.NotFound {
		t.Fatal("explicit clear rejected as missing", err)
	}
	for _, c := range []*v1.RoutingContext{nil, {TargetPort: 65536}, {VlessRoute: 65536}, {SourceIps: []string{"fe80::1%eth0"}}, {Network: v1.Network(1)}} {
		_, err := h.TestRoute(ctx, &v1.TestRouteRequest{InstanceId: "unknown", Context: c})
		if status.Code(err) != codes.InvalidArgument {
			t.Fatal(c, err)
		}
	}
	_, err = h.TestRoute(ctx, &v1.TestRouteRequest{InstanceId: "unknown", Context: &v1.RoutingContext{}})
	if status.Code(err) != codes.NotFound {
		t.Fatal("empty native context was rejected", err)
	}
	_, err = h.ListRoutingRules(ctx, &v1.ListRoutingRulesRequest{InstanceId: "unknown", PageSize: ^uint32(0)})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
}
