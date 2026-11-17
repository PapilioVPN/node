//go:build integration && linux

package grpcapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"reflect"
	"testing"
	"time"

	"go.uber.org/fx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	instances "github.com/PapilioVPN/node/internal/apps/core/engines/instances/domain"
	outboundapi "github.com/PapilioVPN/node/internal/apps/xray/network/outbounds/infra/api"
	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/domain"
	routingv1 "github.com/PapilioVPN/node/internal/apps/xray/network/routing/grpcapi/proto/v1"
	routingxray "github.com/PapilioVPN/node/internal/apps/xray/network/routing/infra/api"
	trafficxray "github.com/PapilioVPN/node/internal/apps/xray/network/traffic/infra/api"
	infoapi "github.com/PapilioVPN/node/internal/apps/xray/runtime/info/infra/api"
	onlineapi "github.com/PapilioVPN/node/internal/apps/xray/users/online/infra/api"
	"github.com/PapilioVPN/node/internal/bootstrap"
	xrayservice "github.com/PapilioVPN/node/internal/bootstrap/xrayapi/proto/v1"
	rpc "github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/testutil"
)

// Native and mTLS paths share one isolated engine fixture and independent oracles.
func TestXrayRoutingProbePreservesNativeFieldsProjectionAndEffects(t *testing.T) {
	for _, network := range []string{"unix", "tcp"} {
		t.Run(network, func(t *testing.T) {
			type webhookEvent struct {
				Email    string `json:"email"`
				Inbound  string `json:"inboundTag"`
				Outbound string `json:"outboundTag"`
			}
			events := make(chan webhookEvent, 4)
			webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer r.Body.Close()
				var event webhookEvent
				if r.Method != http.MethodPost || json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&event) != nil {
					t.Error("invalid native webhook")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				select {
				case events <- event:
				default:
					t.Error("unexpected extra webhook: possible probe replay")
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			t.Cleanup(webhook.Close)
			certificates := testutil.NewCertificates(t)
			first, firstAddress := testutil.ConfiguredXray(t, certificates.Directory, "edge-a")
			second, secondAddress := testutil.ConfiguredXray(t, certificates.Directory, "edge-b")
			testutil.ConfigureXrayAPI(t, &first, network, []string{"RoutingService"})
			testutil.ConfigureXrayAPI(t, &second, network, []string{"RoutingService"})
			firstConfig := configureXrayProbeRouting(t, first, webhook.URL)
			secondConfig := configureXrayProbeRouting(t, second, webhook.URL)
			cfg := certificates.Config()
			cfg.Instances = []settings.Instance{first, second}
			cfg.Communications.GRPC.Permissions.TestEngineRouting = true
			var server *rpc.Server
			var cleanup struct {
				fx.In
				Closers []io.Closer `group:"module_resources"`
			}
			application := bootstrap.New(cfg, "test", fx.NopLogger, fx.Populate(&server, &cleanup))
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
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			if err := testutil.XrayTraffic(ctx, firstAddress, secondAddress); err != nil {
				t.Fatal(err)
			}
			bindings := make(map[instances.InstanceID]services.RoutingProber)
			for _, instance := range cfg.Instances {
				client := nativeXrayAPIClient(t, instance)
				bindings[instance.ID] = client
				// Test-only readiness polling. Production probes themselves never retry.
				ticker := time.NewTicker(20 * time.Millisecond)
				t.Cleanup(ticker.Stop)
				for {
					_, err := client.ReadRoutingRules(ctx)
					if err == nil {
						break
					}
					if status.Code(err) != codes.Unavailable {
						t.Fatalf("routing API readiness: %v", err)
					}
					select {
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					case <-ticker.C:
					}
				}
				ticker.Stop()
			}
			service := services.NewRoutingProbeService(bindings)
			input := domain.RoutingContext{InboundTag: " exact>>>in ", Network: domain.RoutingTCP,
				SourceIPs: []netip.Addr{netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr("::ffff:192.0.2.1"), netip.MustParseAddr("192.0.2.1")}, SourcePort: 60000,
				TargetIPs: []netip.Addr{netip.MustParseAddr("2001:db8::1")}, TargetPort: 65535, TargetDomain: "details.invalid",
				LocalIPs: []netip.Addr{netip.MustParseAddr("192.0.2.2")}, LocalPort: 1, VLESSRoute: 65535,
				Protocol: " opaque-protocol ", User: " route-user ", Attributes: map[string]string{" opaque ": " value "}}
			result, err := service.TestRoute(ctx, "edge-a", domain.RoutingProbe{Context: input})
			expected := input
			// Native AsProtobufMessage omits VLESSRoute; never echo the request.
			expected.VLESSRoute = 0
			if err != nil || result.InstanceID != "edge-a" || result.Result.Route.OutboundTag != "edge-a-out-a" || !reflect.DeepEqual(result.Result.Route.Context, expected) || result.Result.AcknowledgedAt.IsZero() {
				t.Fatalf("native full context was narrowed/fabricated: %+v %v", result, err)
			}
			client := xrayservice.NewXrayServiceClient(certificates.GRPCClient(t, server.Addr().String()))
			wireInput := &routingv1.RoutingContext{
				InboundTag: " exact>>>in ", Network: routingv1.RoutingNetwork_ROUTING_NETWORK_TCP,
				SourceIps: []string{"192.0.2.1", "::ffff:192.0.2.1", "192.0.2.1"}, SourcePort: 60000,
				TargetIps: []string{"2001:0db8::1"}, TargetPort: 65535, TargetDomain: "details.invalid",
				LocalIps: []string{"192.0.2.2"}, LocalPort: 1, VlessRoute: 65535,
				Protocol: " opaque-protocol ", User: " route-user ", Attributes: map[string]string{" opaque ": " value "},
			}
			response, err := client.TestRoute(ctx, &routingv1.TestRouteRequest{InstanceId: "edge-a", Context: wireInput})
			wireExpected := proto.Clone(wireInput).(*routingv1.RoutingContext)
			wireExpected.TargetIps[0], wireExpected.VlessRoute = "2001:db8::1", 0
			if err != nil || response.InstanceId != "edge-a" || response.GetRoute().GetOutboundTag() != "edge-a-out-a" || response.AcknowledgedAtUnixMs <= 0 || !proto.Equal(response.Route.Context, wireExpected) {
				t.Fatalf("mTLS probe lost native context/selection/projection: %v %v", response, err)
			}
			withoutRoute := input
			withoutRoute.VLESSRoute = 0
			if result, err := service.TestRoute(ctx, "edge-a", domain.RoutingProbe{Context: withoutRoute}); err != nil || result.Result.Route.OutboundTag != "edge-a-out-b" {
				t.Fatalf("native VLESSRoute input did not affect selection: %+v %v", result, err)
			}
			input.TargetDomain = "priority.invalid"
			for _, publish := range []bool{false, true} {
				result, err := service.TestRoute(ctx, "edge-a", domain.RoutingProbe{Context: input, FieldSelectors: []string{"out"}, PublishResult: publish})
				if err != nil || result.Result.Route.OutboundTag != "edge-a-out-a" || !reflect.DeepEqual(result.Result.Route.Context, domain.RoutingContext{}) {
					t.Fatalf("native projection changed or input was echoed: %+v %v", result, err)
				}
				select {
				case event := <-events:
					if event.Email != " route-user " || event.Inbound != " exact>>>in " || event.Outbound != "edge-a-out-a" {
						t.Fatalf("webhook context/instance changed: %+v", event)
					}
				case <-ctx.Done():
					t.Fatal("native probe did not fire the configured webhook")
				}
			}
			wireInput.TargetDomain = "priority.invalid"
			for _, publish := range []bool{false, true} {
				response, err := client.TestRoute(ctx, &routingv1.TestRouteRequest{InstanceId: "edge-a", Context: wireInput, FieldSelectors: []string{"out"}, PublishResult: publish})
				if err != nil || response.GetRoute().GetOutboundTag() != "edge-a-out-a" || !proto.Equal(response.Route.Context, &routingv1.RoutingContext{}) {
					t.Fatalf("mTLS probe did not preserve native projection: %v %v", response, err)
				}
				select {
				case event := <-events:
					if event.Email != " route-user " || event.Inbound != " exact>>>in " || event.Outbound != "edge-a-out-a" {
						t.Fatalf("mTLS probe changed webhook identity/effects: %+v", event)
					}
				case <-ctx.Done():
					t.Fatal("mTLS probe did not fire its native webhook")
				}
			}
			// A caller-side receive limit can discard Node's valid acknowledgement
			// after the native webhook effect. Missing details never mean no effect.
			lost, lostErr := client.TestRoute(ctx, &routingv1.TestRouteRequest{InstanceId: "edge-a", Context: wireInput}, grpc.MaxCallRecvMsgSize(1))
			if lost != nil || status.Code(lostErr) != codes.ResourceExhausted {
				t.Fatalf("expected caller-side acknowledgement loss: %v %v", lost, lostErr)
			}
			for _, detail := range status.Convert(lostErr).Details() {
				if failure, ok := detail.(*routingv1.TestRouteFailure); ok && failure.Outcome == routingv1.RoutingProbeOutcome_ROUTING_PROBE_OUTCOME_NOT_EXECUTED {
					t.Fatal("lost external acknowledgement claimed no effects")
				}
			}
			select {
			case event := <-events:
				if event.Outbound != "edge-a-out-a" || event.Email != " route-user " {
					t.Fatalf("lost-reply effect changed identity: %+v", event)
				}
			case <-ctx.Done():
				t.Fatal("caller-side reply loss erased the native webhook effect")
			}
			// The native outbound manager sorts this fixture's two candidate tags.
			// Consecutive probes advance round-robin, despite PublishResult=false.
			for _, item := range []struct {
				id     instances.InstanceID
				suffix string
			}{{"edge-a", "a"}, {"edge-a", "b"}, {"edge-a", "a"}, {"edge-b", "a"}, {"edge-a", "b"}} {
				probe := domain.RoutingProbe{Context: domain.RoutingContext{Network: domain.RoutingTCP, TargetDomain: "pool.invalid"}}
				result, err := service.TestRoute(ctx, item.id, probe)
				if err != nil || result.Result.Route.OutboundTag != string(item.id)+"-out-"+item.suffix {
					t.Fatalf("probe did not retain native strategy progression/instance isolation: %+v %v", result, err)
				}
			}
			for _, item := range []struct {
				id     string
				suffix string
			}{{"edge-a", "a"}, {"edge-b", "b"}} {
				response, err := client.TestRoute(ctx, &routingv1.TestRouteRequest{InstanceId: item.id, Context: &routingv1.RoutingContext{Network: routingv1.RoutingNetwork_ROUTING_NETWORK_TCP, TargetDomain: "pool.invalid"}})
				if err != nil || response.InstanceId != item.id || response.GetRoute().GetOutboundTag() != item.id+"-out-"+item.suffix {
					t.Fatalf("mTLS probe crossed instances or changed strategy effects: %v %v", response, err)
				}
			}
			result, err = service.TestRoute(ctx, "edge-a", domain.RoutingProbe{Context: domain.RoutingContext{TargetDomain: "details.invalid", VLESSRoute: 65535}, FieldSelectors: []string{"unknown"}})
			if err != nil || !reflect.DeepEqual(result.Result.Route, domain.RouteResult{}) || result.Result.AcknowledgedAt.IsZero() {
				t.Fatalf("empty native projection became missing route: %+v %v", result, err)
			}
			result, err = service.TestRoute(ctx, "edge-a", domain.RoutingProbe{})
			if err == nil || !errors.Is(err, domain.ErrRoutingProbeUnknown) || !reflect.DeepEqual(result, domain.InstanceRoutingProbe{}) {
				t.Fatalf("native no-match became successful route or known no effect: %+v %v", result, err)
			}
			if response, err := client.TestRoute(ctx, &routingv1.TestRouteRequest{InstanceId: "edge-a", Context: &routingv1.RoutingContext{}}); response != nil {
				t.Fatal("native no-match became an mTLS success")
			} else {
				assertRoutingProbeFailure(t, err, codes.Unavailable, routingv1.RoutingProbeOutcome_ROUTING_PROBE_OUTCOME_UNKNOWN)
			}
			if err := testutil.XrayTraffic(ctx, firstAddress, secondAddress); err != nil {
				t.Fatal(err)
			}
			for _, item := range []struct {
				instance settings.Instance
				original []byte
			}{{first, firstConfig}, {second, secondConfig}} {
				current, err := os.ReadFile(item.instance.Xray.ConfigFile)
				if err != nil || !bytes.Equal(current, item.original) {
					t.Fatalf("probe modified config: %v", err)
				}
			}
		})
	}
}

type nativeXrayAPI struct {
	*trafficxray.Counters
	*outboundapi.Observer
	*onlineapi.Online
	*infoapi.Reader
	*routingxray.Router
}

func nativeXrayAPIClient(t *testing.T, instance settings.Instance) *nativeXrayAPI {
	t.Helper()
	client := testutil.XrayAPIClient(t, instance.Xray.API)
	return &nativeXrayAPI{Counters: &trafficxray.Counters{API: client}, Observer: &outboundapi.Observer{API: client}, Online: &onlineapi.Online{API: client}, Reader: &infoapi.Reader{API: client}, Router: &routingxray.Router{API: client}}
}

func configureXrayProbeRouting(t *testing.T, instance settings.Instance, webhook string) []byte {
	t.Helper()
	configureXrayRouting(t, instance)
	content, err := os.ReadFile(instance.Xray.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatal(err)
	}
	id := string(instance.ID)
	config["routing"] = json.RawMessage(fmt.Sprintf(`{
	 "rules":[
	  {"type":"field","domain":["full:priority.invalid"],"outboundTag":%q,"ruleTag":"priority","webhook":{"url":%q}},
	  {"type":"field","domain":["full:details.invalid"],"vlessRoute":"65535","outboundTag":%q,"ruleTag":"details"},
	  {"type":"field","domain":["full:pool.invalid"],"balancerTag":"pool","ruleTag":"pool"},
	  {"type":"field","network":"tcp,udp","outboundTag":%q,"ruleTag":"catch-all"}
	 ],
	 "balancers":[{"tag":"pool","selector":[%q],"strategy":{"type":"roundrobin"}}]
	}`, id+"-out-a", webhook, id+"-out-a", id+"-out-b", id+"-out-"))
	content, err = json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(instance.Xray.ConfigFile, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return content
}
