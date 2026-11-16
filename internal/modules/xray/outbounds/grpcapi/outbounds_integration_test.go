//go:build integration && linux

package grpcapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/fx"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/PapilioVPN/node/internal/bootstrap"
	xrayservice "github.com/PapilioVPN/node/internal/bootstrap/xrayapi/proto/v1"
	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	infov1 "github.com/PapilioVPN/node/internal/modules/xray/info/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/modules/xray/outbounds/app/services"
	"github.com/PapilioVPN/node/internal/modules/xray/outbounds/domain"
	xrayv1 "github.com/PapilioVPN/node/internal/modules/xray/outbounds/grpcapi/proto/v1"
	outboundapi "github.com/PapilioVPN/node/internal/modules/xray/outbounds/infra/api"
	routingv1 "github.com/PapilioVPN/node/internal/modules/xray/routing/grpcapi/proto/v1"
	rpc "github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/shared/testutil"
)

func TestXrayOutboundObservationsRetainOrdinaryAndBurstFactsWithoutNewProbes(t *testing.T) {
	for _, network := range []string{"tcp", "unix"} {
		for _, burst := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/burst=%v", network, burst), func(t *testing.T) {
				var hits atomic.Int64
				probe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					hits.Add(1)
					w.WriteHeader(http.StatusNoContent)
				}))
				t.Cleanup(probe.Close)
				certificates := testutil.NewCertificates(t)
				first, firstAddress := testutil.ConfiguredXray(t, certificates.Directory, "edge-a")
				second, secondAddress := testutil.ConfiguredXray(t, certificates.Directory, "edge-b")
				testutil.ConfigureXrayAPI(t, &first, network, []string{"ObservatoryService"})
				testutil.ConfigureXrayAPI(t, &second, network, []string{"ObservatoryService"})
				originalA := configureXrayObservatory(t, first, probe.URL, burst, []string{"edge-a-"})
				originalB := configureXrayObservatory(t, second, probe.URL, burst, []string{})
				cfg := certificates.Config()
				cfg.Communications.GRPC.Permissions.ReadEngineOutbounds = true
				cfg.Instances = []settings.Instance{first, second}
				var server *rpc.Server
				var cleanup struct {
					fx.In
					Closers []io.Closer `group:"module_resources"`
				}
				application := bootstrap.New(cfg, "test", fx.NopLogger, fx.Populate(&cleanup, &server))
				if err := application.Start(t.Context()); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := application.Stop(context.Background()); err != nil {
						t.Error(err)
					}
					if err := server.Close(); err != nil {
						t.Error(err)
					}
					for _, closer := range cleanup.Closers {
						if err := closer.Close(); err != nil {
							t.Error(err)
						}
					}
					testutil.AssertTCPPortsReleased(t, firstAddress, secondAddress)
					if network == "tcp" {
						testutil.AssertTCPPortsReleased(t, first.Xray.API.Address, second.Xray.API.Address)
					}
				})
				// Independent test-owned native clients remain observation oracles;
				// public mTLS calls use the production module's existing API clients.
				firstAPI, secondAPI := &outboundapi.Observer{API: testutil.XrayAPIClient(t, first.Xray.API)}, &outboundapi.Observer{API: testutil.XrayAPIClient(t, second.Xray.API)}
				service := services.NewOutboundStatusService(map[instances.InstanceID]services.OutboundStatusReader{"edge-a": firstAPI, "edge-b": secondAPI})
				ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
				defer cancel()
				awaitOutboundObservations(t, ctx, service, "edge-a", 2)
				empty := awaitOutboundObservations(t, ctx, service, "edge-b", 0)
				if empty.Statuses.ObservedAt.IsZero() {
					t.Fatal("empty native observation lost receipt time")
				}
				good, err := service.GetOutboundStatus(ctx, "edge-a", "edge-a-good")
				if err != nil || good.Observation.Status == nil || !good.Observation.Status.Alive || good.Observation.Status.DelayMS < 0 {
					t.Fatalf("actual successful probe was lost: %+v %v", good, err)
				}
				bad, err := service.GetOutboundStatus(ctx, "edge-a", "edge-a-bad")
				if err != nil || bad.Observation.Status == nil || bad.Observation.Status.Alive {
					t.Fatalf("actual blackhole failure was lost: %+v %v", bad, err)
				}
				for _, item := range []domain.OutboundStatus{*good.Observation.Status, *bad.Observation.Status} {
					if burst {
						h := item.HealthPing
						if h == nil || h.TotalSamples <= 0 || h.FailedSamples < 0 || h.FailedSamples > h.TotalSamples || item.DelayMS != h.AverageNS/int64(time.Millisecond) || item.LastSeenUnixSec != 0 || item.LastTryUnixSec != 0 || item.LastErrorReason != "" {
							t.Fatalf("actual burst presence/units differ: %+v", item)
						}
						if item.Alive && (h.AverageNS <= 0 || h.MinNS > h.AverageNS || h.MaxNS < h.AverageNS) {
							t.Fatal("successful burst duration statistics lost")
						}
					} else {
						if item.HealthPing != nil || item.LastTryUnixSec <= 0 {
							t.Fatal("ordinary status fabricated burst statistics or lost timestamp")
						}
						if item.Alive && (item.LastSeenUnixSec != item.LastTryUnixSec || item.LastErrorReason != "") {
							t.Fatal("ordinary success timestamps changed")
						}
						if !item.Alive && (item.LastSeenUnixSec != 0 || item.LastErrorReason == "" || item.DelayMS != 99999999) {
							t.Fatal("native ordinary failure sentinel/diagnostics lost")
						}
					}
				}
				before := hits.Load()
				for range 10 {
					result, err := service.ListOutboundStatuses(ctx, "edge-a")
					if err != nil || len(result.Statuses.Items) != 2 || result.Statuses.Items[0].Tag != "edge-a-bad" || result.Statuses.Items[1].Tag != "edge-a-good" {
						t.Fatalf("actual collection changed: %+v %v", result, err)
					}
				}
				// Ordinary's next scheduled pass is one hour away. Burst owns its
				// independent scheduler, so no fixed request-count promise applies.
				if before == 0 || !burst && hits.Load() != before {
					t.Fatal("observation reads started new probes")
				}
				public := xrayservice.NewXrayServiceClient(certificates.GRPCClient(t, server.Addr().String()))
				wire, err := public.ListOutboundStatuses(ctx, &xrayv1.ListOutboundStatusesRequest{InstanceId: "edge-a"})
				if err != nil || wire.InstanceId != "edge-a" || len(wire.Statuses) != 2 || wire.Statuses[0].OutboundTag != "edge-a-bad" || wire.Statuses[1].OutboundTag != "edge-a-good" || wire.ObservedAtUnixMs <= 0 {
					t.Fatalf("mTLS outbound collection differs: %v %v", wire, err)
				}
				for _, tag := range []string{"edge-a-good", "edge-a-bad"} {
					value, err := public.GetOutboundStatus(ctx, &xrayv1.GetOutboundStatusRequest{InstanceId: "edge-a", OutboundTag: proto.String(tag)})
					if err != nil || value.InstanceId != "edge-a" || value.OutboundTag != tag || value.Status == nil || value.Status.OutboundTag != tag || value.Status.Alive != (tag == "edge-a-good") || value.ObservedAtUnixMs <= 0 {
						t.Fatalf("mTLS actual native status differs: %v %v", value, err)
					}
					if burst {
						h := value.Status.HealthPing
						if h == nil || h.TotalSamples <= 0 || value.Status.DelayMs != h.AverageNs/int64(time.Millisecond) || value.Status.LastSeenUnixSec != 0 || value.Status.LastTryUnixSec != 0 {
							t.Fatal("mTLS burst presence/units differ")
						}
					} else if value.Status.HealthPing != nil || value.Status.LastTryUnixSec <= 0 {
						t.Fatal("mTLS ordinary presence/time differs")
					}
				}
				for _, tag := range []string{"", " edge-a-good ", "missing"} {
					value, err := public.GetOutboundStatus(ctx, &xrayv1.GetOutboundStatusRequest{InstanceId: "edge-a", OutboundTag: proto.String(tag)})
					if err != nil || value.Status != nil || value.OutboundTag != tag || value.ObservedAtUnixMs <= 0 {
						t.Fatalf("mTLS absent observation became zero/normalized tag: %v %v", value, err)
					}
				}
				wire, err = public.ListOutboundStatuses(ctx, &xrayv1.ListOutboundStatusesRequest{InstanceId: "edge-b"})
				if err != nil || wire.InstanceId != "edge-b" || len(wire.Statuses) != 0 || wire.ObservedAtUnixMs <= 0 {
					t.Fatal("mTLS empty/cross-instance result differs")
				}
				if _, err := public.GetOutboundStatus(ctx, &xrayv1.GetOutboundStatusRequest{InstanceId: "edge-a"}); status.Code(err) != codes.InvalidArgument {
					t.Fatal("mTLS omitted tag selected anonymous observation")
				}
				if _, err := public.GetRuntimeInfo(ctx, &infov1.GetRuntimeInfoRequest{InstanceId: "edge-a"}); status.Code(err) != codes.PermissionDenied {
					t.Fatal("outbound grant enabled runtime reads")
				}
				if _, err := public.ListRoutingRules(ctx, &routingv1.ListRoutingRulesRequest{InstanceId: "edge-a"}); status.Code(err) != codes.PermissionDenied {
					t.Fatal("outbound grant enabled routing reads")
				}
				if !burst && hits.Load() != before {
					t.Fatal("mTLS observations started new probes")
				}
				for _, selection := range []struct {
					id  instances.InstanceID
					tag string
				}{{"edge-a", " edge-a-good "}, {"edge-a", "missing"}, {"edge-b", "edge-a-good"}} {
					value, err := service.GetOutboundStatus(ctx, selection.id, selection.tag)
					if err != nil || value.Observation.Status != nil || value.Observation.ObservedAt.IsZero() {
						t.Fatal("missing/exact-tag/cross-instance observation became zero delay")
					}
				}
				if err := testutil.XrayTraffic(ctx, firstAddress, secondAddress); err != nil {
					t.Fatal(err)
				}
				for _, item := range []struct {
					instance settings.Instance
					content  []byte
				}{{first, originalA}, {second, originalB}} {
					content, err := os.ReadFile(item.instance.Xray.ConfigFile)
					if err != nil || !bytes.Equal(content, item.content) {
						t.Fatal("outbound observation rewrote native configuration")
					}
				}
			})
		}
	}
}

func configureXrayObservatory(t *testing.T, instance settings.Instance, url string, burst bool, selectors []string) []byte {
	t.Helper()
	content, err := os.ReadFile(instance.Xray.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatal(err)
	}
	id := string(instance.ID)
	config["outbounds"] = json.RawMessage(fmt.Sprintf(`[{"tag":%q,"protocol":"freedom"},{"tag":%q,"protocol":"blackhole"}]`, id+"-good", id+"-bad"))
	selectorJSON, err := json.Marshal(selectors)
	if err != nil {
		t.Fatal(err)
	}
	// Both native probe variants target only this test's owned loopback server.
	if burst {
		config["burstObservatory"] = json.RawMessage(fmt.Sprintf(`{"subjectSelector":%s,"pingConfig":{"destination":%q,"interval":"10s","sampling":1,"timeout":"500ms"}}`, selectorJSON, url))
	} else {
		config["observatory"] = json.RawMessage(fmt.Sprintf(`{"subjectSelector":%s,"probeURL":%q,"probeInterval":"1h","enableConcurrency":true}`, selectorJSON, url))
	}
	content, err = json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(instance.Xray.ConfigFile, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return content
}

// Only tests poll native readiness/probe completion. Production never retries.
func awaitOutboundObservations(t *testing.T, ctx context.Context, service *services.OutboundStatusService, id instances.InstanceID, count int) domain.InstanceOutboundStatuses {
	t.Helper()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		result, err := service.ListOutboundStatuses(ctx, id)
		if err == nil && len(result.Statuses.Items) == count {
			return result
		}
		if err != nil && status.Code(err) != codes.Unavailable {
			t.Fatalf("native outbound readiness/probe failed: %v", err)
		}
		select {
		case <-ctx.Done():
			t.Fatal("native outbound observations did not arrive")
		case <-ticker.C:
		}
	}
}
