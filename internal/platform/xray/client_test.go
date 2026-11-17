package xray

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc"

	outboundapi "github.com/PapilioVPN/node/internal/apps/xray/network/outbounds/infra/api"
	routingdomain "github.com/PapilioVPN/node/internal/apps/xray/network/routing/domain"
	routingxray "github.com/PapilioVPN/node/internal/apps/xray/network/routing/infra/api"
	"github.com/PapilioVPN/node/internal/apps/xray/network/traffic/domain"
	trafficxray "github.com/PapilioVPN/node/internal/apps/xray/network/traffic/infra/api"
	infoapi "github.com/PapilioVPN/node/internal/apps/xray/runtime/info/infra/api"
	userdomain "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	userxray "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/api"
)

func TestAPIConstructionAndCanceledAdmissionCreateNoNativeChannel(t *testing.T) {
	client, err := NewClient(Config{Network: "unix", Address: "/unused/api.sock", RequestTimeout: time.Second, MaxReceiveBytes: 4096, MaxConcurrent: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	adapter := &outboundapi.Observer{API: client}
	info := &infoapi.Reader{API: client}
	counters := &trafficxray.Counters{API: client}
	router := &routingxray.Router{API: client}
	users := &userxray.Adapter{API: client}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := info.ReadRuntimeInfo(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled admission: %v", err)
	}
	if client.connection != nil {
		t.Fatal("construction/canceled admission allocated native workers")
	}
	if _, err := adapter.ReadOutboundStatuses(ctx); !errors.Is(err, context.Canceled) || client.connection != nil {
		t.Fatal("canceled outbound observation created native workers")
	}
	if _, err := router.ReadRoutingRules(ctx); !errors.Is(err, context.Canceled) || client.connection != nil {
		t.Fatal("canceled routing read created native workers")
	}
	if _, err := router.ReadBalancerInfo(ctx, "pool"); !errors.Is(err, context.Canceled) || client.connection != nil {
		t.Fatal("canceled balancer read created native workers")
	}
	if _, err := router.TestRoute(ctx, routingdomain.RoutingProbe{}); !errors.Is(err, context.Canceled) || errors.Is(err, routingdomain.ErrRoutingProbeUnknown) || client.connection != nil {
		t.Fatalf("pre-canceled probe created native effects/workers: %v", err)
	}
	if _, err := router.OverrideBalancerTarget(ctx, routingdomain.BalancerOverride{BalancerTag: "pool"}); !errors.Is(err, context.Canceled) || errors.Is(err, routingdomain.ErrRoutingMutationUnknown) || client.connection != nil {
		t.Fatalf("pre-canceled override created native effects/workers: %v", err)
	}

	if _, err := router.RemoveRoutingRule(ctx, "rule"); !errors.Is(err, context.Canceled) || errors.Is(err, routingdomain.ErrRoutingMutationUnknown) || client.connection != nil {
		t.Fatalf("pre-canceled removal created workers/effects: %v", err)
	}
	if _, err := router.RemoveRoutingRule(t.Context(), "\xff"); !errors.Is(err, routingdomain.ErrInvalidRoutingRuleTag) || errors.Is(err, routingdomain.ErrRoutingMutationUnknown) || client.connection != nil {
		t.Fatalf("unrepresentable removal created workers/effects: %v", err)
	}

	for _, mode := range []routingdomain.RoutingRuleMode{0, 255} {
		if _, err := router.ApplyRoutingRule(t.Context(), routingdomain.PreparedRoutingRule{Config: []byte{1}}, mode); !errors.Is(err, routingdomain.ErrInvalidRoutingRuleMode) || errors.Is(err, routingdomain.ErrRoutingMutationUnknown) || client.connection != nil {
			t.Fatalf("invalid mode created workers/effects: %v", err)
		}
	}
	if _, err := router.ApplyRoutingRule(t.Context(), routingdomain.PreparedRoutingRule{}, routingdomain.AppendRoutingRule); !errors.Is(err, routingdomain.ErrInvalidPreparedRoutingRule) || client.connection != nil {
		t.Fatal("empty compiled input created native workers")
	}
	if _, err := router.ApplyRoutingRule(ctx, routingdomain.PreparedRoutingRule{Config: []byte{1}}, routingdomain.ReplaceRoutingRules); !errors.Is(err, context.Canceled) || errors.Is(err, routingdomain.ErrRoutingMutationUnknown) || client.connection != nil {
		t.Fatal("canceled application created native workers/effects")
	}
	for _, selection := range []routingdomain.BalancerOverride{{BalancerTag: "\xff"}, {BalancerTag: "pool", Target: "\xff"}} {
		if _, err := router.OverrideBalancerTarget(t.Context(), selection); !errors.Is(err, routingdomain.ErrInvalidBalancerOverride) || errors.Is(err, routingdomain.ErrRoutingMutationUnknown) || client.connection != nil {
			t.Fatalf("invalid override allocated native workers/effects: %v", err)
		}
	}
	addition := userdomain.InboundUserAddition{InboundTag: "in", User: userdomain.InboundUser{Email: "user", Account: userdomain.UserAccount{Protocol: userdomain.VLESS, VLESS: &userdomain.VLESSAccount{ID: "test-only"}}}}
	if _, err := users.AddInboundUser(ctx, addition); !errors.Is(err, context.Canceled) || errors.Is(err, userdomain.ErrUserMutationUnknown) || client.connection != nil {
		t.Fatalf("canceled addition created native effects/workers: %v", err)
	}
	if _, err := users.ReadInboundUserCount(ctx, "in"); !errors.Is(err, context.Canceled) || client.connection != nil {
		t.Fatalf("canceled count created native workers: %v", err)
	}
	if _, err := users.ReadInboundUser(ctx, userdomain.InboundUserSelection{InboundTag: "in", Email: "user"}); !errors.Is(err, context.Canceled) || client.connection != nil {
		t.Fatal("canceled user lookup created native workers")
	}
	if _, err := users.ReadInboundUsers(ctx, "in"); !errors.Is(err, context.Canceled) || client.connection != nil {
		t.Fatal("canceled user collection created native workers")
	}
	selection := domain.TrafficSelection{Scope: domain.InboundTraffic, Name: "proxy", Direction: domain.Uplink}
	if _, err := counters.ReadTrafficCounter(ctx, selection); !errors.Is(err, context.Canceled) || client.connection != nil {
		t.Fatalf("canceled traffic admission created a channel: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := info.ReadRuntimeInfo(t.Context()); !errors.Is(err, grpc.ErrClientConnClosing) {
		t.Fatalf("Close before first read: %v", err)
	}
	if client.connection != nil {
		t.Fatal("closed admission created native channel")
	}
	if _, err := adapter.ReadOutboundStatuses(t.Context()); !errors.Is(err, grpc.ErrClientConnClosing) || client.connection != nil {
		t.Fatal("closed outbound observation created native workers")
	}
	if _, err := router.ReadRoutingRules(t.Context()); !errors.Is(err, grpc.ErrClientConnClosing) || client.connection != nil {
		t.Fatal("closed routing read created native workers")
	}
	if _, err := router.ReadBalancerInfo(t.Context(), "pool"); !errors.Is(err, grpc.ErrClientConnClosing) || client.connection != nil {
		t.Fatal("closed balancer read created native workers")
	}
	if _, err := router.TestRoute(t.Context(), routingdomain.RoutingProbe{}); !errors.Is(err, grpc.ErrClientConnClosing) || errors.Is(err, routingdomain.ErrRoutingProbeUnknown) || client.connection != nil {
		t.Fatalf("closed probe created native effects/workers: %v", err)
	}
	if _, err := router.OverrideBalancerTarget(t.Context(), routingdomain.BalancerOverride{BalancerTag: "pool"}); !errors.Is(err, grpc.ErrClientConnClosing) || errors.Is(err, routingdomain.ErrRoutingMutationUnknown) || client.connection != nil {
		t.Fatalf("closed override created native effects/workers: %v", err)
	}

	if _, err := router.RemoveRoutingRule(t.Context(), "rule"); !errors.Is(err, grpc.ErrClientConnClosing) || errors.Is(err, routingdomain.ErrRoutingMutationUnknown) || client.connection != nil {
		t.Fatalf("closed removal created workers/effects: %v", err)
	}

	if _, err := router.ApplyRoutingRule(t.Context(), routingdomain.PreparedRoutingRule{Config: []byte{1}}, routingdomain.AppendRoutingRule); !errors.Is(err, grpc.ErrClientConnClosing) || errors.Is(err, routingdomain.ErrRoutingMutationUnknown) || client.connection != nil {
		t.Fatalf("closed application created workers/effects: %v", err)
	}
	if _, err := users.AddInboundUser(t.Context(), addition); !errors.Is(err, grpc.ErrClientConnClosing) || errors.Is(err, userdomain.ErrUserMutationUnknown) || client.connection != nil {
		t.Fatalf("closed addition created native effects/workers: %v", err)
	}
	if _, err := users.ReadInboundUserCount(t.Context(), "in"); !errors.Is(err, grpc.ErrClientConnClosing) || client.connection != nil {
		t.Fatalf("closed count created native workers: %v", err)
	}
	if _, err := users.ReadInboundUser(t.Context(), userdomain.InboundUserSelection{InboundTag: "in", Email: "user"}); !errors.Is(err, grpc.ErrClientConnClosing) || client.connection != nil {
		t.Fatal("closed user lookup created native workers")
	}
	if _, err := users.ReadInboundUsers(t.Context(), "in"); !errors.Is(err, grpc.ErrClientConnClosing) || client.connection != nil {
		t.Fatal("closed user collection created native workers")
	}
	if _, err := counters.ReadTrafficCounter(t.Context(), selection); !errors.Is(err, grpc.ErrClientConnClosing) || client.connection != nil {
		t.Fatalf("closed traffic admission created a channel: %v", err)
	}
}
