package xray

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"go.uber.org/fx"
	"google.golang.org/grpc"

	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
	instances "github.com/PapilioVPN/node/internal/modules/instances/domain"
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
	if err := resources.Close(); err != nil {
		t.Fatalf("final cleanup after OnStop: %v", err)
	}
}
