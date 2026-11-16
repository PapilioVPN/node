package xray

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"go.uber.org/fx"
	"google.golang.org/grpc"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	infoservices "github.com/PapilioVPN/node/internal/modules/xray/info/app/services"
	routingdomain "github.com/PapilioVPN/node/internal/modules/xray/routing/domain"
	routingxray "github.com/PapilioVPN/node/internal/modules/xray/routing/infra/api"
	trafficservices "github.com/PapilioVPN/node/internal/modules/xray/traffic/app/services"
	"github.com/PapilioVPN/node/internal/modules/xray/traffic/domain"
	userdomain "github.com/PapilioVPN/node/internal/modules/xray/users/domain"
	userxray "github.com/PapilioVPN/node/internal/modules/xray/users/infra/api"
	"github.com/PapilioVPN/node/internal/settings"
)

func TestModuleClosesAPIAdmissionAndExportsFinalCleanup(t *testing.T) {
	configuration := []settings.Instance{{
		ID: "edge", Engine: instances.Xray,
		Xray: &settings.Xray{API: &settings.XrayAPI{
			Network: "tcp", Address: "127.0.0.1:12345", RequestTimeout: time.Second,
			MaxReceiveBytes: 4096, MaxConcurrent: 1,
		}},
	}}
	var cleanup struct {
		fx.In
		Closers []io.Closer `group:"module_resources"`
	}
	application := fx.New(fx.NopLogger, fx.Supply(configuration), Module(false), fx.Populate(&cleanup))
	if err := application.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = application.Stop(context.Background())
		for _, closer := range cleanup.Closers {
			_ = closer.Close()
		}
	})
	if len(cleanup.Closers) != 1 {
		t.Fatalf("API final cleanup contributions: %d", len(cleanup.Closers))
	}
	resources, ok := cleanup.Closers[0].(*apiResources)
	if !ok {
		t.Fatal("API owner was not exported for final cleanup")
	}
	service := infoservices.NewRuntimeInfoService(resources.runtimeReaders)
	userAdapter := &userxray.Adapter{API: resources.clients[0]}
	router := &routingxray.Router{API: resources.clients[0]}
	traffic := newTrafficBindings(resources).Values[0]
	trafficReads := trafficservices.NewTrafficCounterService(map[instances.InstanceID]trafficservices.TrafficCounterReader{traffic.InstanceID: traffic.Reader})
	trafficResets := trafficservices.NewTrafficCounterResetService(map[instances.InstanceID]trafficservices.TrafficCounterResetter{traffic.InstanceID: traffic.Resetter})
	trafficCollections := trafficservices.NewTrafficCountersService(map[instances.InstanceID]trafficservices.TrafficCountersReader{traffic.InstanceID: traffic.Collection})
	scopeResets := trafficservices.NewTrafficCountersResetService(map[instances.InstanceID]trafficservices.TrafficResetBinding{traffic.InstanceID: {Reader: traffic.Collection, Resetter: traffic.Resetter, Timeout: traffic.Timeout}})
	pairedReads := trafficservices.NewTrafficPairService(map[instances.InstanceID]trafficservices.TrafficPairReadBinding{traffic.InstanceID: {Reader: traffic.Reader, Timeout: traffic.Timeout}})
	pairedResets := trafficservices.NewTrafficPairResetService(map[instances.InstanceID]trafficservices.TrafficPairResetBinding{traffic.InstanceID: {Resetter: traffic.Resetter, Timeout: traffic.Timeout}})
	if err := application.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetRuntimeInfo(t.Context(), "edge"); !errors.Is(err, grpc.ErrClientConnClosing) {
		t.Fatalf("OnStop did not close API admission: %v", err)
	}
	if _, err := newOutboundStatusService(resources).ListOutboundStatuses(t.Context(), "edge"); !errors.Is(err, grpc.ErrClientConnClosing) {
		t.Fatalf("closed module admitted outbound list: %v", err)
	}
	if _, err := newOutboundStatusService(resources).GetOutboundStatus(t.Context(), "edge", "out"); !errors.Is(err, grpc.ErrClientConnClosing) {
		t.Fatalf("closed module admitted outbound lookup: %v", err)
	}
	if _, err := trafficReads.GetTrafficCounter(t.Context(), "edge", domain.TrafficSelection{Scope: domain.InboundTraffic, Name: "proxy", Direction: domain.Uplink}); !errors.Is(err, grpc.ErrClientConnClosing) {
		t.Fatalf("OnStop did not close traffic admission: %v", err)
	}
	if _, err := trafficResets.ResetTrafficCounter(t.Context(), "edge", domain.TrafficSelection{Scope: domain.InboundTraffic, Name: "proxy", Direction: domain.Uplink}); !errors.Is(err, grpc.ErrClientConnClosing) || errors.Is(err, domain.ErrTrafficResetUnknown) {
		t.Fatalf("OnStop did not close reset admission before effects: %v", err)
	}
	if _, err := trafficCollections.ListTrafficCounters(t.Context(), "edge", domain.UserTraffic); !errors.Is(err, grpc.ErrClientConnClosing) {
		t.Fatalf("OnStop did not close collection admission: %v", err)
	}
	if report, err := scopeResets.ResetTrafficCounters(t.Context(), "edge", domain.UserTraffic, 1); !errors.Is(err, grpc.ErrClientConnClosing) || len(report.Reset.Items) != 0 {
		t.Fatalf("OnStop did not close collection reset admission before effects: %+v %v", report, err)
	}
	if _, err := pairedReads.GetTrafficPair(t.Context(), "edge", domain.TrafficPairSelection{Scope: domain.UserTraffic, Name: "user"}); !errors.Is(err, grpc.ErrClientConnClosing) {
		t.Fatalf("pair read admission after close: %v", err)
	}
	if report, err := pairedResets.ResetTrafficPair(t.Context(), "edge", domain.TrafficPairSelection{Scope: domain.UserTraffic, Name: "user"}); !errors.Is(err, grpc.ErrClientConnClosing) || report.Items[0].State != domain.TrafficResetNotApplied || report.Items[1].State != domain.TrafficResetNotAttempted {
		t.Fatalf("pair reset admission after close: %+v %v", report, err)
	}
	if _, err := newOnlineService(resources).GetUserOnlineStats(t.Context(), "edge", "reader"); !errors.Is(err, grpc.ErrClientConnClosing) {
		t.Fatalf("OnStop did not close online count admission: %v", err)
	}
	if _, err := newOnlineService(resources).ListUserOnlineIPs(t.Context(), "edge", "reader"); !errors.Is(err, grpc.ErrClientConnClosing) {
		t.Fatalf("OnStop did not close online IP admission: %v", err)
	}
	if _, err := newOnlineService(resources).ListOnlineUsers(t.Context(), "edge"); !errors.Is(err, grpc.ErrClientConnClosing) {
		t.Fatalf("OnStop did not close online user admission: %v", err)
	}
	if err := resources.Close(); err != nil {
		t.Fatalf("final cleanup after OnStop: %v", err)
	}
	if _, err := userAdapter.RemoveInboundUser(t.Context(), userdomain.InboundUserSelection{InboundTag: "in", Email: "user"}); !errors.Is(err, grpc.ErrClientConnClosing) || errors.Is(err, userdomain.ErrUserMutationUnknown) {
		t.Fatalf("closed native API admitted user removal: %v", err)
	}
	addition := userdomain.InboundUserAddition{InboundTag: "in", User: userdomain.InboundUser{Email: "user", Account: userdomain.UserAccount{Protocol: userdomain.VLESS, VLESS: &userdomain.VLESSAccount{ID: "test-only"}}}}
	if _, err := userAdapter.AddInboundUser(t.Context(), addition); !errors.Is(err, grpc.ErrClientConnClosing) || errors.Is(err, userdomain.ErrUserMutationUnknown) {
		t.Fatalf("closed native API admitted user addition: %v", err)
	}
	if _, err := userAdapter.ReadInboundUserCount(t.Context(), "in"); !errors.Is(err, grpc.ErrClientConnClosing) {
		t.Fatalf("closed native API admitted user count: %v", err)
	}
	if _, err := userAdapter.ReadInboundUser(t.Context(), userdomain.InboundUserSelection{InboundTag: "in", Email: "user"}); !errors.Is(err, grpc.ErrClientConnClosing) {
		t.Fatalf("closed module admitted user lookup: %v", err)
	}
	if _, err := userAdapter.ReadInboundUsers(t.Context(), "in"); !errors.Is(err, grpc.ErrClientConnClosing) {
		t.Fatalf("closed module admitted user listing: %v", err)
	}
	if _, err := router.ReadRoutingRules(t.Context()); !errors.Is(err, grpc.ErrClientConnClosing) {
		t.Fatalf("closed native API admitted routing list: %v", err)
	}
	if _, err := router.ReadBalancerInfo(t.Context(), "pool"); !errors.Is(err, grpc.ErrClientConnClosing) {
		t.Fatalf("closed native API admitted balancer read: %v", err)
	}
	if _, err := router.TestRoute(t.Context(), routingdomain.RoutingProbe{}); !errors.Is(err, grpc.ErrClientConnClosing) || errors.Is(err, routingdomain.ErrRoutingProbeUnknown) {
		t.Fatalf("closed native API admitted a probe or claimed effects: %v", err)
	}
	if _, err := router.OverrideBalancerTarget(t.Context(), routingdomain.BalancerOverride{BalancerTag: "pool", Target: ""}); !errors.Is(err, grpc.ErrClientConnClosing) || errors.Is(err, routingdomain.ErrRoutingMutationUnknown) {
		t.Fatalf("closed native API admitted a routing change or claimed effects: %v", err)
	}

	if _, err := router.RemoveRoutingRule(t.Context(), "rule"); !errors.Is(err, grpc.ErrClientConnClosing) || errors.Is(err, routingdomain.ErrRoutingMutationUnknown) {
		t.Fatalf("closed module admitted rule removal/claimed effects: %v", err)
	}
	if _, err := router.ApplyRoutingRule(t.Context(), routingdomain.PreparedRoutingRule{Config: []byte{1}}, routingdomain.AppendRoutingRule); !errors.Is(err, grpc.ErrClientConnClosing) || errors.Is(err, routingdomain.ErrRoutingMutationUnknown) {
		t.Fatalf("module close admitted rule addition/claimed effects: %v", err)
	}

}
