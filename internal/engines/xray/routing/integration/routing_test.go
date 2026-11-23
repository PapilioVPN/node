package integration_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/engines/xray"
	configsv1 "github.com/PapilioVPN/node/internal/engines/xray/configs/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/xray/instances/lifecycle"
	routingv1 "github.com/PapilioVPN/node/internal/engines/xray/routing/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const configJSON = `{
  "log": {"loglevel": "none"},
  "outbounds": [
    {"protocol":"freedom","tag":"direct"},
    {"protocol":"blackhole","tag":"pool-a"},
    {"protocol":"blackhole","tag":"pool-b"}
  ],
  "routing": {
    "balancers": [
      {"tag":"pool / +","selector":["pool-"],"strategy":{"type":"roundRobin"}},
      {"tag":"empty","selector":["absent-"],"strategy":{"type":"roundRobin"}}
    ],
    "rules":[
      {"type":"field","network":"tcp","ruleTag":"z-first","outboundTag":"direct"},
      {"type":"field","network":"udp","balancerTag":"pool / +"},
      {"type":"field","network":"tcp","outboundTag":"direct"},
      {"type":"field","network":"udp","ruleTag":"a-last","outboundTag":"pool-a"}
    ]
  }
}`

const base = "/v1/xray/instances/primary/routing"

func TestRoutingReads(t *testing.T) {
	executable := os.Getenv("NODE_TEST_XRAY")
	if executable == "" {
		t.Skip("set NODE_TEST_XRAY to Xray v26.11.9")
	}
	dir, err := os.MkdirTemp("", "node-routing-")
	require(t, err)
	t.Cleanup(func() { require(t, os.RemoveAll(dir)) })
	path := filepath.Join(dir, "xray.json")
	require(t, os.WriteFile(path, []byte(configJSON), 0600))

	router := chi.NewRouter()
	server := grpc.NewServer()
	var instance *lifecycle.Service
	config := settings.Xray{ID: "primary", Executable: executable, ConfigFile: path, RuntimeDirectory: dir}
	app := fx.New(fx.NopLogger, xray.Module, fx.Supply(router, server, config), fx.Populate(&instance))
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	require(t, app.Start(ctx))
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		require(t, app.Stop(stopCtx))
	})
	conn := connectManagement(t, server)
	client := routingv1.NewXrayRoutingServiceClient(conn)
	configs := configsv1.NewXrayConfigsServiceClient(conn)
	result, err := configs.ValidateConfig(ctx, &configsv1.ValidateXrayConfigRequest{Id: "primary"})
	require(t, err)
	if !result.GetValid() {
		t.Fatalf("routing fixture rejected by config validation: %v", result)
	}

	list := &routingv1.XrayRoutingRulesRequest{Id: "primary"}
	balancer := &routingv1.XrayBalancerInfoRequest{Id: "primary", Tag: "pool / +"}
	balancerPath := base + "/balancer?tag=" + url.QueryEscape(balancer.Tag)
	get := func(path string, code int) []byte {
		t.Helper()
		res := httptest.NewRecorder()
		router.ServeHTTP(res, httptest.NewRequestWithContext(ctx, http.MethodGet, path, nil))
		if res.Code != code || res.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: HTTP %d %s", path, res.Code, res.Body)
		}
		return res.Body.Bytes()
	}
	get(base+"/rules", http.StatusServiceUnavailable)
	_, err = client.GetBalancerInfo(ctx, balancer)
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("before start: %v", err)
	}
	get("/v1/xray/instances/missing/routing/rules", http.StatusNotFound)
	_, err = client.ListRoutingRules(ctx, &routingv1.XrayRoutingRulesRequest{Id: "missing"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("unknown instance: %v", err)
	}

	_, err = instance.Start(ctx, "primary")
	require(t, err)
	nativeConn, err := instance.NativeConnection("primary")
	require(t, err)

	want := [][2]string{{"z-first", "direct"}, {"", ""}, {"", "direct"}, {"a-last", "pool-a"}}
	checkRules := func(response *routingv1.XrayRoutingRules) {
		t.Helper()
		got := make([][2]string, 0, len(response.GetRules()))
		for _, rule := range response.GetRules() {
			got = append(got, [2]string{rule.GetRuleTag(), rule.GetOutboundTag()})
		}
		if !slices.Equal(got, want) {
			t.Fatalf("rule list = %v, want %v", got, want)
		}
	}
	rules, err := client.ListRoutingRules(ctx, list)
	require(t, err)
	checkRules(rules)
	var httpRules routingv1.XrayRoutingRules
	require(t, json.Unmarshal(get(base+"/rules", http.StatusOK), &httpRules))
	checkRules(&httpRules)

	checkBalancer := func(override string) {
		t.Helper()
		verify := func(info *routingv1.XrayBalancerInfo) {
			t.Helper()
			if info.OverrideTarget == nil || info.GetOverrideTarget() != override ||
				!slices.Equal(info.GetReportedTargets().GetTags(), []string{"pool-a", "pool-b"}) {
				t.Fatalf("balancer report = %v, want override %q and both strategy targets", info, override)
			}
		}
		info, err := client.GetBalancerInfo(ctx, balancer)
		require(t, err)
		verify(info)
		var httpInfo routingv1.XrayBalancerInfo
		require(t, json.Unmarshal(get(balancerPath, http.StatusOK), &httpInfo))
		verify(&httpInfo)
	}
	checkBalancer("")
	// Set the override through stock Xray only to prepare the read fixture.
	request := new(emptypb.Empty)
	fields := protowire.AppendString(protowire.AppendTag(nil, 1, protowire.BytesType), balancer.Tag)
	fields = protowire.AppendString(protowire.AppendTag(fields, 2, protowire.BytesType), "direct")
	request.ProtoReflect().SetUnknown(fields)
	const nativeBase = "/xray.app.router.command.RoutingService/"
	require(t, nativeConn.Invoke(ctx, nativeBase+"OverrideBalancerTarget", request, &emptypb.Empty{}))
	checkBalancer("direct")

	info, err := client.GetBalancerInfo(ctx, &routingv1.XrayBalancerInfoRequest{Id: "primary", Tag: "empty"})
	require(t, err)
	if info.GetReportedTargets() == nil || len(info.GetReportedTargets().GetTags()) != 0 {
		t.Fatalf("zero reported targets: %v", info)
	}
	empty := get(base+"/balancer?tag=empty", http.StatusOK)
	if !strings.Contains(string(empty), `"tags":[]`) {
		t.Fatalf("empty native target report: %s", empty)
	}

	err = nativeConn.Invoke(ctx, nativeBase+"GetBalancerInfo", wrapperspb.String("missing"), &emptypb.Empty{})
	if st := status.Convert(err); st.Code() != codes.Unknown || st.Message() != "app/router: cannot find tag" {
		t.Fatalf("pinned Xray missing-balancer response: %v", err)
	}
	_, err = client.GetBalancerInfo(ctx, &routingv1.XrayBalancerInfoRequest{Id: "primary", Tag: "missing"})
	if status.Code(err) != codes.NotFound || status.Convert(err).Message() != "Xray balancer not found" {
		t.Fatalf("unknown balancer: %v", err)
	}
	if body := get(base+"/balancer?tag=missing", http.StatusNotFound); string(body) != "Xray balancer not found\n" {
		t.Fatalf("unknown balancer body: %s", body)
	}
	get(base+"/balancer?tag=a&tag=b", http.StatusBadRequest)
	get(base+"/balancer?tag=%FF", http.StatusBadRequest)
	_, err = client.GetBalancerInfo(ctx, &routingv1.XrayBalancerInfoRequest{Id: "primary"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("missing balancer tag: %v", err)
	}

	verifyRoutingControl(t, ctx, router, client, balancer)

	current, err := instance.NativeConnection("primary")
	require(t, err)
	if current != nativeConn {
		t.Fatal("routing reads replaced the active native connection")
	}
	_, err = instance.Stop(ctx, "primary")
	require(t, err)
	_, err = instance.Start(ctx, "primary")
	require(t, err)
	checkBalancer("")

	// With no router configuration Xray uses DefaultRouter and reports no
	// override or strategy data. Preserve that absence on both transports.
	_, err = instance.Stop(ctx, "primary")
	require(t, err)
	require(t, os.WriteFile(path, []byte(`{"log":{"loglevel":"none"}}`), 0600))
	_, err = instance.Start(ctx, "primary")
	require(t, err)
	rules, err = client.ListRoutingRules(ctx, list)
	require(t, err)
	if len(rules.GetRules()) != 0 || strings.TrimSpace(string(get(base+"/rules", http.StatusOK))) != `{"rules":[]}` {
		t.Fatalf("default router listed rules: %v", rules)
	}
	info, err = client.GetBalancerInfo(ctx, balancer)
	require(t, err)
	if info.OverrideTarget != nil || info.GetReportedTargets() != nil ||
		strings.TrimSpace(string(get(balancerPath, http.StatusOK))) != `{}` {
		t.Fatalf("default router reported balancer fields: %v", info)
	}

}

func connectManagement(t *testing.T, server *grpc.Server) *grpc.ClientConn {
	t.Helper()
	listener := bufconn.Listen(64 << 10)
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		if err := <-done; err != nil && err != grpc.ErrServerStopped {
			t.Error(err)
		}
	})
	conn, err := grpc.NewClient("passthrough:///xray-routing",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }))
	require(t, err)
	t.Cleanup(func() { require(t, conn.Close()) })
	return conn
}

func require(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
