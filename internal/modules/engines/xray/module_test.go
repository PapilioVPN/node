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
	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
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
	service := newRuntimeInfoService(resources)
	if err := application.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetRuntimeInfo(t.Context(), "edge"); !errors.Is(err, grpc.ErrClientConnClosing) {
		t.Fatalf("OnStop did not close API admission: %v", err)
	}
	if _, err := newTrafficCounterService(resources).GetTrafficCounter(t.Context(), "edge", domain.TrafficSelection{Scope: domain.InboundTraffic, Name: "proxy", Direction: domain.Uplink}); !errors.Is(err, grpc.ErrClientConnClosing) {
		t.Fatalf("OnStop did not close traffic admission: %v", err)
	}
	if _, err := newTrafficCounterResetService(resources).ResetTrafficCounter(t.Context(), "edge", domain.TrafficSelection{Scope: domain.InboundTraffic, Name: "proxy", Direction: domain.Uplink}); !errors.Is(err, grpc.ErrClientConnClosing) || errors.Is(err, domain.ErrTrafficResetUnknown) {
		t.Fatalf("OnStop did not close reset admission before effects: %v", err)
	}
	if _, err := newTrafficCountersService(resources).ListTrafficCounters(t.Context(), "edge", domain.UserTraffic); !errors.Is(err, grpc.ErrClientConnClosing) {
		t.Fatalf("OnStop did not close collection admission: %v", err)
	}
	if report, err := newTrafficCountersResetService(configuration, resources).ResetTrafficCounters(t.Context(), "edge", domain.UserTraffic, 1); !errors.Is(err, grpc.ErrClientConnClosing) || len(report.Reset.Items) != 0 {
		t.Fatalf("OnStop did not close collection reset admission before effects: %+v %v", report, err)
	}
	pairedReads, pairedResets := newTrafficPairServices(configuration, resources)
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
	if _, err := newUserRemovalService(resources).RemoveInboundUser(t.Context(), "edge", domain.InboundUserSelection{InboundTag: "in", Email: "user"}); !errors.Is(err, grpc.ErrClientConnClosing) || errors.Is(err, domain.ErrUserMutationUnknown) {
		t.Fatalf("closed native API admitted user removal: %v", err)
	}
	addition := domain.InboundUserAddition{InboundTag: "in", User: domain.InboundUser{Email: "user", Account: domain.UserAccount{Protocol: domain.VLESS, VLESS: &domain.VLESSAccount{ID: "test-only"}}}}
	if _, err := newUserAdditionService(resources).AddInboundUser(t.Context(), "edge", addition); !errors.Is(err, grpc.ErrClientConnClosing) || errors.Is(err, domain.ErrUserMutationUnknown) {
		t.Fatalf("closed native API admitted user addition: %v", err)
	}
	if _, err := newUserCountService(resources).GetInboundUserCount(t.Context(), "edge", "in"); !errors.Is(err, grpc.ErrClientConnClosing) {
		t.Fatalf("closed native API admitted user count: %v", err)
	}
}
