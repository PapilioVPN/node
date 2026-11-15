//go:build integration && linux

package bootstrap_test

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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/bootstrap"
	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/app/services"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
	xrayapi "github.com/PapilioVPN/node/internal/modules/engines/xray/infra/api"
	rpc "github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/shared/testutil"
)

// Actual native/process coverage; the public probe adapter is a separate increment.
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
			first, firstAddress := configuredXray(t, certificates.Directory, "edge-a")
			second, secondAddress := configuredXray(t, certificates.Directory, "edge-b")
			configureXrayAPI(t, &first, network, []string{"RoutingService"})
			configureXrayAPI(t, &second, network, []string{"RoutingService"})
			firstConfig := configureXrayProbeRouting(t, first, webhook.URL)
			secondConfig := configureXrayProbeRouting(t, second, webhook.URL)
			cfg := certificates.Config()
			cfg.Instances = []settings.Instance{first, second}
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
				assertInstancePortsReleased(t, firstAddress, secondAddress)
				if network == "tcp" {
					assertInstancePortsReleased(t, first.Xray.API.Address, second.Xray.API.Address)
				}
			})
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			if err := xrayTraffic(ctx, firstAddress, secondAddress); err != nil {
				t.Fatal(err)
			}
			bindings := make(map[instances.InstanceID]services.RoutingProber)
			for _, instance := range cfg.Instances {
				client := routingProbeAPIClient(t, instance)
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
			result, err = service.TestRoute(ctx, "edge-a", domain.RoutingProbe{Context: domain.RoutingContext{TargetDomain: "details.invalid", VLESSRoute: 65535}, FieldSelectors: []string{"unknown"}})
			if err != nil || !reflect.DeepEqual(result.Result.Route, domain.RouteResult{}) || result.Result.AcknowledgedAt.IsZero() {
				t.Fatalf("empty native projection became missing route: %+v %v", result, err)
			}
			result, err = service.TestRoute(ctx, "edge-a", domain.RoutingProbe{})
			if err == nil || !errors.Is(err, domain.ErrRoutingProbeUnknown) || !reflect.DeepEqual(result, domain.InstanceRoutingProbe{}) {
				t.Fatalf("native no-match became successful route or known no effect: %+v %v", result, err)
			}
			if err := xrayTraffic(ctx, firstAddress, secondAddress); err != nil {
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

func routingProbeAPIClient(t *testing.T, instance settings.Instance) *xrayapi.Client {
	t.Helper()
	api := instance.Xray.API
	client, err := xrayapi.NewClient(xrayapi.Config{Network: api.Network, Address: api.Address, RequestTimeout: api.RequestTimeout, MaxReceiveBytes: api.MaxReceiveBytes, MaxConcurrent: api.MaxConcurrent})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	})
	return client
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
