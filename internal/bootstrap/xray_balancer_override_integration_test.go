//go:build integration && linux

package bootstrap_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"testing"
	"time"

	"go.uber.org/fx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/PapilioVPN/node/internal/bootstrap"
	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/app/services"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
	xrayv1 "github.com/PapilioVPN/node/internal/modules/engines/xray/grpcapi/proto/v1"
	xrayapi "github.com/PapilioVPN/node/internal/modules/engines/xray/infra/api"
	rpc "github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/shared/testutil"
)

func TestXrayBalancerOverrideAndClearPreserveStrategyIsolationAndConfig(t *testing.T) {
	for _, network := range []string{"unix", "tcp"} {
		t.Run(network, func(t *testing.T) {
			certificates := testutil.NewCertificates(t)
			first, healthA := configuredXray(t, certificates.Directory, "edge-a")
			second, healthB := configuredXray(t, certificates.Directory, "edge-b")
			configureXrayAPI(t, &first, network, []string{"RoutingService"})
			configureXrayAPI(t, &second, network, []string{"RoutingService"})
			balancedA, configA := configureXrayBalancerOverride(t, first)
			balancedB, configB := configureXrayBalancerOverride(t, second)
			cfg := certificates.Config()
			cfg.Instances = []settings.Instance{first, second}
			cfg.Communications.GRPC.Permissions.ManageEngineRouting = true
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
				assertInstancePortsReleased(t, healthA, healthB, balancedA, balancedB)
				if network == "tcp" {
					assertInstancePortsReleased(t, first.Xray.API.Address, second.Xray.API.Address)
				}
			})
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			if err := xrayTraffic(ctx, healthA, healthB); err != nil {
				t.Fatal(err)
			}
			clients := map[instances.InstanceID]*xrayapi.Client{
				"edge-a": nativeXrayAPIClient(t, first), "edge-b": nativeXrayAPIClient(t, second),
			}
			for _, client := range clients {
				ticker := time.NewTicker(20 * time.Millisecond)
				t.Cleanup(ticker.Stop)
				for {
					if _, err := client.ReadBalancerInfo(ctx, "pool"); err == nil {
						break
					} else if status.Code(err) != codes.Unavailable {
						t.Fatalf("balancer API readiness: %v", err)
					}
					select {
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					case <-ticker.C:
					}
				}
				ticker.Stop()
			}
			firstAPI, secondAPI := clients["edge-a"], clients["edge-b"]
			baseline, err := firstAPI.ReadRoutingRules(ctx)
			if err != nil {
				t.Fatal(err)
			}
			service := services.NewBalancerOverrideService(map[instances.InstanceID]services.BalancerOverrider{"edge-a": firstAPI, "edge-b": secondAPI})
			probe := domain.RoutingProbe{Context: domain.RoutingContext{Network: domain.RoutingTCP, TargetDomain: "pool.invalid"}}
			assertRoute := func(client *xrayapi.Client, target string) {
				t.Helper()
				result, err := client.TestRoute(ctx, probe)
				if err != nil || result.Route.OutboundTag != target {
					t.Fatalf("actual balancer selection changed: %+v %v want %q", result, err, target)
				}
			}
			assign := func(target string) {
				t.Helper()
				before := time.Now()
				result, err := service.OverrideBalancerTarget(ctx, "edge-a", domain.BalancerOverride{BalancerTag: "pool", Target: target})
				if err != nil || result.InstanceID != "edge-a" || result.Override.Target != target || result.Override.BalancerTag != "pool" || result.Acknowledgement.AcknowledgedAt.Before(before) || result.Acknowledgement.AcknowledgedAt.After(time.Now()) {
					t.Fatalf("override completion/identity: %+v %v", result, err)
				}
				info, err := firstAPI.ReadBalancerInfo(ctx, "pool")
				if err != nil || info.OverrideTarget == nil || *info.OverrideTarget != target {
					t.Fatalf("actual override differs: %+v %v", info, err)
				}
			}
			// One real HTTP selection and two probes leave the two-target strategy
			// at its second target. Overrides must not reset or advance that index.
			assertBalancedHTTP(t, ctx, balancedA, true)
			assertRoute(firstAPI, "edge-a-out-b")
			assertRoute(firstAPI, "edge-a-out-a")
			assign("blocked") // A valid native target outside the pool's selector.
			assertRoute(firstAPI, "blocked")
			assertBalancedHTTP(t, ctx, balancedA, false)
			assertRoute(secondAPI, "edge-b-out-a")
			assertBalancedHTTP(t, ctx, balancedB, true)
			assign("")
			assertRoute(firstAPI, "edge-a-out-b")
			assertBalancedHTTP(t, ctx, balancedA, true)
			for _, target := range []string{"missing-outbound", "   ", " exact>>>outside "} {
				assign(target)
				assertRoute(firstAPI, target) // Native completion never checks existence.
			}
			assign("")
			ack, err := firstAPI.OverrideBalancerTarget(ctx, domain.BalancerOverride{BalancerTag: "missing", Target: "blocked"})
			if err == nil || !errors.Is(err, domain.ErrRoutingMutationUnknown) || !ack.AcknowledgedAt.IsZero() {
				t.Fatalf("generic missing tag became success/known no-effect: %+v %v", ack, err)
			}

			// Exercise the process-owned module binding through a real mTLS client.
			// Only the write grant is enabled; readback is an independent native witness.
			public := xrayClient(t, server, certificates)
			publicAssign := func(target string) {
				t.Helper()
				before := time.Now().UnixMilli()
				response, err := public.OverrideBalancerTarget(ctx, &xrayv1.OverrideBalancerTargetRequest{InstanceId: "edge-a", BalancerTag: "pool", Target: proto.String(target)})
				if err != nil || response.InstanceId != "edge-a" || response.BalancerTag != "pool" || response.Target != target || response.AcknowledgedAtUnixMs < before || response.AcknowledgedAtUnixMs > time.Now().UnixMilli() {
					t.Fatalf("public override acknowledgement changed: %v %v", response, err)
				}
				info, err := firstAPI.ReadBalancerInfo(ctx, "pool")
				if err != nil || info.OverrideTarget == nil || *info.OverrideTarget != target {
					t.Fatalf("public override not applied natively: %+v %v", info, err)
				}
			}
			publicAssign("blocked")
			assertBalancedHTTP(t, ctx, balancedA, false)
			_, omitted := public.OverrideBalancerTarget(ctx, &xrayv1.OverrideBalancerTargetRequest{InstanceId: "edge-a", BalancerTag: "pool"})
			assertRoutingMutationFailure(t, omitted, codes.InvalidArgument, xrayv1.RoutingMutationOutcome_ROUTING_MUTATION_OUTCOME_NOT_APPLIED)
			info, err := firstAPI.ReadBalancerInfo(ctx, "pool")
			if err != nil || info.OverrideTarget == nil || *info.OverrideTarget != "blocked" {
				t.Fatalf("omitted target cleared an override: %+v %v", info, err)
			}
			publicAssign("") // Presence survives protobuf encoding even when empty.
			assertBalancedHTTP(t, ctx, balancedA, true)
			for _, target := range []string{"missing-outbound", "   ", " exact>>>outside "} {
				publicAssign(target)
				assertRoute(firstAPI, target)
			}
			publicAssign("")
			_, missing := public.OverrideBalancerTarget(ctx, &xrayv1.OverrideBalancerTargetRequest{InstanceId: "edge-a", BalancerTag: "missing", Target: proto.String("blocked")})
			assertRoutingMutationFailure(t, missing, codes.Unavailable, xrayv1.RoutingMutationOutcome_ROUTING_MUTATION_OUTCOME_UNKNOWN)
			// Discard a completed response at the external caller's receive boundary.
			// Its local error cannot establish NOT_APPLIED or justify a retry.
			response, lost := public.OverrideBalancerTarget(ctx, &xrayv1.OverrideBalancerTargetRequest{InstanceId: "edge-a", BalancerTag: "pool", Target: proto.String("blocked")}, grpc.MaxCallRecvMsgSize(1))
			if response != nil || status.Code(lost) != codes.ResourceExhausted {
				t.Fatalf("test failed to discard the public acknowledgement: %v %v", response, lost)
			}
			info, err = firstAPI.ReadBalancerInfo(ctx, "pool")
			if err != nil || info.OverrideTarget == nil || *info.OverrideTarget != "blocked" {
				t.Fatalf("lost acknowledgement did not follow actual execution: %+v %v", info, err)
			}
			assertBalancedHTTP(t, ctx, balancedA, false)
			publicAssign("")
			assertBalancedHTTP(t, ctx, balancedA, true)
			for _, item := range []struct {
				client *xrayapi.Client
				tag    string
			}{{firstAPI, "other-pool"}, {secondAPI, "pool"}} {
				info, err := item.client.ReadBalancerInfo(ctx, item.tag)
				if err != nil || info.OverrideTarget == nil || *info.OverrideTarget != "" {
					t.Fatalf("unrelated balancer/instance changed: %+v %v", info, err)
				}
			}
			current, err := firstAPI.ReadRoutingRules(ctx)
			if err != nil || !reflect.DeepEqual(current.Items, baseline.Items) {
				t.Fatalf("override changed routing rules: %+v %v", current, err)
			}
			assertBalancedHTTP(t, ctx, balancedA, true)
			if err := xrayTraffic(ctx, healthA, healthB); err != nil {
				t.Fatal(err)
			}
			for _, item := range []struct {
				instance settings.Instance
				original []byte
			}{{first, configA}, {second, configB}} {
				content, err := os.ReadFile(item.instance.Xray.ConfigFile)
				if err != nil || !bytes.Equal(content, item.original) {
					t.Fatalf("override changed native config: %v", err)
				}
			}
		})
	}
}

func assertBalancedHTTP(t *testing.T, ctx context.Context, address string, allowed bool) {
	t.Helper()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "balanced-traffic") }))
	defer target.Close()
	proxy, err := url.Parse("http://" + address)
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	success := false
	if err == nil {
		body, readError := io.ReadAll(io.LimitReader(response.Body, 64))
		response.Body.Close()
		success = readError == nil && response.StatusCode == http.StatusOK && string(body) == "balanced-traffic"
	}
	if success != allowed {
		t.Fatalf("balanced traffic allowed=%v want %v, error=%v", success, allowed, err)
	}
}

func configureXrayBalancerOverride(t *testing.T, instance settings.Instance) (string, []byte) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address, port := listener.Addr().String(), listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(instance.Xray.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatal(err)
	}
	var inbounds []json.RawMessage
	if err := json.Unmarshal(config["inbounds"], &inbounds); err != nil {
		t.Fatal(err)
	}
	inbounds = append(inbounds, json.RawMessage(fmt.Sprintf(`{"tag":"balanced-http","listen":"127.0.0.1","port":%d,"protocol":"http","settings":{}}`, port)))
	config["inbounds"], err = json.Marshal(inbounds)
	if err != nil {
		t.Fatal(err)
	}
	id := string(instance.ID)
	config["outbounds"] = json.RawMessage(fmt.Sprintf(`[{"tag":%q,"protocol":"freedom"},{"tag":%q,"protocol":"freedom"},{"tag":"blocked","protocol":"blackhole"}]`, id+"-out-a", id+"-out-b"))
	config["routing"] = json.RawMessage(fmt.Sprintf(`{
	 "rules":[
	  {"type":"field","inboundTag":["balanced-http"],"balancerTag":"pool","ruleTag":"balanced"},
	  {"type":"field","domain":["full:pool.invalid"],"balancerTag":"pool","ruleTag":"probe"},
	  {"type":"field","network":"tcp,udp","outboundTag":%q,"ruleTag":"fallback"}
	 ],
	 "balancers":[
	  {"tag":"pool","selector":[%q],"strategy":{"type":"roundrobin"}},
	  {"tag":"other-pool","selector":[%q],"strategy":{"type":"roundrobin"}}
	 ]}`, id+"-out-a", id+"-out-", id+"-out-"))
	content, err = json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(instance.Xray.ConfigFile, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return address, content
}
