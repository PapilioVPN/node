package bootstrap

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"go.uber.org/fx"
	"google.golang.org/grpc"

	instances "github.com/PapilioVPN/node/internal/apps/core/engines/instances/domain"
	outboundservices "github.com/PapilioVPN/node/internal/apps/xray/network/outbounds/app/services"
	outboundapi "github.com/PapilioVPN/node/internal/apps/xray/network/outbounds/infra/api"
	routingdomain "github.com/PapilioVPN/node/internal/apps/xray/network/routing/domain"
	routingxray "github.com/PapilioVPN/node/internal/apps/xray/network/routing/infra/api"
	trafficowner "github.com/PapilioVPN/node/internal/apps/xray/network/traffic"
	trafficservices "github.com/PapilioVPN/node/internal/apps/xray/network/traffic/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/network/traffic/domain"
	trafficapi "github.com/PapilioVPN/node/internal/apps/xray/network/traffic/infra/api"
	infoservices "github.com/PapilioVPN/node/internal/apps/xray/runtime/info/app/services"
	infoapi "github.com/PapilioVPN/node/internal/apps/xray/runtime/info/infra/api"
	userdomain "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	userxray "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/api"
	onlineservices "github.com/PapilioVPN/node/internal/apps/xray/users/online/app/services"
	onlinexray "github.com/PapilioVPN/node/internal/apps/xray/users/online/infra/api"
	nativeapi "github.com/PapilioVPN/node/internal/platform/xray"
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
	application := fx.New(fx.NopLogger, fx.Supply(configuration), xrayServices(false), fx.Populate(&cleanup))
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
	resources, ok := cleanup.Closers[0].(*nativeapi.Resources)
	if !ok {
		t.Fatal("API owner was not exported for final cleanup")
	}
	client, _ := resources.Client("edge")
	service := infoservices.NewRuntimeInfoService(map[instances.InstanceID]infoservices.RuntimeInfoReader{"edge": &infoapi.Reader{API: client}})
	outbounds := outboundservices.NewOutboundStatusService(map[instances.InstanceID]outboundservices.OutboundStatusReader{"edge": &outboundapi.Observer{API: client}})
	online := onlineservices.NewOnlineService(map[instances.InstanceID]onlineservices.OnlineReader{"edge": &onlinexray.Online{API: client}})
	userAdapter := &userxray.Adapter{API: client}
	router := &routingxray.Router{API: client}
	counters := &trafficapi.Counters{API: client}
	traffic := trafficowner.BackendBinding{InstanceID: "edge", Reader: counters, Collection: counters, Resetter: counters, Timeout: time.Second}
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
	if _, err := outbounds.ListOutboundStatuses(t.Context(), "edge"); !errors.Is(err, grpc.ErrClientConnClosing) {
		t.Fatalf("closed module admitted outbound list: %v", err)
	}
	if _, err := outbounds.GetOutboundStatus(t.Context(), "edge", "out"); !errors.Is(err, grpc.ErrClientConnClosing) {
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
	if _, err := online.GetUserOnlineStats(t.Context(), "edge", "reader"); !errors.Is(err, grpc.ErrClientConnClosing) {
		t.Fatalf("OnStop did not close online count admission: %v", err)
	}
	if _, err := online.ListUserOnlineIPs(t.Context(), "edge", "reader"); !errors.Is(err, grpc.ErrClientConnClosing) {
		t.Fatalf("OnStop did not close online IP admission: %v", err)
	}
	if _, err := online.ListOnlineUsers(t.Context(), "edge"); !errors.Is(err, grpc.ErrClientConnClosing) {
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
