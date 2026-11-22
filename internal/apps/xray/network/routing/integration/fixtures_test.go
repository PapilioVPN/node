//go:build integration && linux

package integration_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	routing "github.com/PapilioVPN/node/internal/apps/xray/network/routing"
	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/app/services"
	v1 "github.com/PapilioVPN/node/internal/apps/xray/network/routing/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/infra/configuration"
	inboundconfiguration "github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/infra/configuration"
	configservices "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/app/services"
	onlineservices "github.com/PapilioVPN/node/internal/apps/xray/users/online/app/services"
	"github.com/PapilioVPN/node/internal/bootstrap"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/shared/mtls"
	"github.com/PapilioVPN/node/internal/testutil"
	"github.com/nats-io/nats.go"
	native "github.com/xtls/xray-core/app/router/command"
	stats "github.com/xtls/xray-core/app/stats/command"
	"go.uber.org/fx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type listenerLog struct {
	slog.Handler
	mu        sync.Mutex
	addresses map[string]string
	failures  []string
}

func (l *listenerLog) Handle(_ context.Context, r slog.Record) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if r.Level >= slog.LevelError {
		var fields []string
		r.Attrs(func(a slog.Attr) bool { fields = append(fields, a.String()); return true })
		l.failures = append(l.failures, fmt.Sprint(r.Message, fields))
	}
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "address" {
			l.addresses[r.Message] = a.Value.String()
		}
		return true
	})
	return nil
}

type fixture struct {
	address    string
	http       *http.Client
	grpc       v1.RoutingServiceClient
	nats       *nats.Conn
	native     native.RoutingServiceClient
	nativeConn *grpc.ClientConn
	proxy      string
	target     *httptest.Server
	config     settings.Config
	restarter  configservices.Restarter
}

func reserve(t *testing.T) (string, net.Listener) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l.Addr().String(), l
}
func newFixture(t *testing.T, pki testutil.PKI, broker, transport, network string, ruleCount int, webhook string, options ...func(*settings.Config)) *fixture {
	t.Helper()
	binary := os.Getenv("XRAY_TEST_BINARY")
	if binary == "" {
		t.Fatal("XRAY_TEST_BINARY must select verified v26.11.9")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "routing-payload") }))
	t.Cleanup(target.Close)
	f := &fixture{target: target}
	config := settings.Config{StartupTimeout: 8 * time.Second, ShutdownTimeout: 20 * time.Second}
	config.Communications.Requests.Timeout = 3 * time.Second
	reservations := []net.Listener{}
	var actualAPIs []settings.XrayAPI
	for _, id := range []string{"edge-a", "edge-b"} {
		directory := t.TempDir()
		proxy, l := reserve(t)
		reservations = append(reservations, l)
		nativeAddress := ""
		if network == "unix" {
			nativeAddress = filepath.Join(directory, "api.sock")
		} else {
			nativeAddress, l = reserve(t)
			reservations = append(reservations, l)
		}
		rules := []any{map[string]any{"type": "field", "ruleTag": "kept", "domain": []string{"full:keep.test"}, "outboundTag": "direct-b"}}
		for i := 0; i < ruleCount; i++ {
			rules = append(rules, map[string]any{"type": "field", "ruleTag": fmt.Sprintf("rule-%04d", i), "inboundTag": []string{fmt.Sprintf("unrelated-%d", i)}, "outboundTag": "direct-a"})
		}
		balanced := map[string]any{"type": "field", "ruleTag": "balanced", "inboundTag": []string{"proxy"}, "balancerTag": "b"}
		if webhook != "" {
			balanced["webhook"] = map[string]any{"url": webhook}
		}
		rules = append(rules, balanced)
		host, port, _ := net.SplitHostPort(proxy)
		var portNumber int
		fmt.Sscan(port, &portNumber)
		source := map[string]any{"log": map[string]any{"loglevel": "none"}, "api": map[string]any{"tag": "api", "listen": nativeAddress, "services": []string{"StatsService", "RoutingService"}}, "stats": map[string]any{}, "inbounds": []any{map[string]any{"tag": "proxy", "listen": host, "port": portNumber, "protocol": "http", "settings": map[string]any{}}}, "outbounds": []any{map[string]any{"tag": "direct-a", "protocol": "freedom"}, map[string]any{"tag": "direct-b", "protocol": "freedom"}, map[string]any{"tag": "block", "protocol": "blackhole"}}, "routing": map[string]any{"domainStrategy": "AsIs", "balancers": []any{map[string]any{"tag": "b", "selector": []string{"direct-"}, "strategy": map[string]any{"type": "roundRobin"}}}, "rules": rules}}
		data, _ := json.Marshal(source)
		path := filepath.Join(directory, "config.json")
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		work := t.TempDir()
		original := bytes.Clone(data)
		t.Cleanup(func() {
			actual, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(actual, original) {
				t.Error("operator config changed", err)
			}
			entries, err := os.ReadDir(work)
			if err != nil || len(entries) != 0 {
				t.Error("converter workspace leaked", err, len(entries))
			}
		})
		config.Instances = append(config.Instances, settings.Instance{ID: id, Engine: "xray", StopTimeout: time.Second, Xray: &settings.Xray{Binary: binary, Configuration: path, WorkDirectory: work, API: &settings.XrayAPI{Network: network, Address: nativeAddress}}})
		actualAPIs = append(actualAPIs, *config.Instances[len(config.Instances)-1].Xray.API)
		if id == "edge-a" {
			f.proxy = proxy
		}
	}
	if transport == "http" || transport == "all" {
		address, l := reserve(t)
		reservations = append(reservations, l)
		config.Communications.HTTP = &settings.HTTP{Address: address, TLS: pki.Server, ReadTimeout: 4 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: time.Second, ShutdownTimeout: time.Second, BodyLimit: 2 << 20, Concurrency: 16}
	}
	if transport == "grpc" || transport == "all" {
		address, l := reserve(t)
		reservations = append(reservations, l)
		config.Communications.GRPC = &settings.GRPC{Address: address, TLS: pki.Server, ConnectionTimeout: 100 * time.Millisecond, ShutdownTimeout: time.Second, MaxRecvMessageSize: 2 << 20, MaxSendMessageSize: 2 << 20, MaxConnections: 8, MaxConcurrentStreams: 8, MaxHeaderListSize: 8192}
	}
	if transport == "nats" || transport == "all" {
		config.Communications.NATS = &settings.NATS{Servers: []string{broker}, TLS: pki.Client, TLSHandshakeFirst: true, ConnectTimeout: time.Second, ShutdownTimeout: time.Second, WriteTimeout: time.Second, ReconnectWait: time.Second, MaxReconnects: 0, ReconnectBufferSize: -1, PingInterval: time.Second, MaxPingsOutstanding: 2}
		config.Communications.Requests.NATS = &settings.NATSRequests{SubjectPrefix: "fixture.routing", PendingMessages: 8, PendingBytes: 2 << 20, ShutdownTimeout: 100 * time.Millisecond}
	}
	for _, option := range options {
		option(&config)
	}
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
	f.config = config
	log := &listenerLog{Handler: slog.NewTextHandler(io.Discard, nil), addresses: map[string]string{}}
	// The shared files remain untouched. These aliases use the same existing
	// instance/config service objects exported to already installed consumers.
	app := bootstrap.New(config, slog.New(log), routing.Module(config.Communications.Requests), fx.Provide(func(s onlineservices.StatusReader) services.StatusReader { return s }), fx.Provide(func(c inboundconfiguration.Compiler) configuration.Compiler { return c }), fx.Invoke(func(r configservices.Restarter) { f.restarter = r }))
	for _, l := range reservations {
		l.Close()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), config.ShutdownTimeout)
		defer cancel()
		if err := app.Stop(ctx); err != nil {
			t.Error(err)
			log.mu.Lock()
			t.Log(log.failures)
			log.mu.Unlock()
		}
	})
	tlsConfig, err := mtls.Client(pki.Client)
	if err != nil {
		t.Fatal(err)
	}
	log.mu.Lock()
	addresses := log.addresses
	log.mu.Unlock()
	if address := addresses["HTTP listening"]; address != "" {
		tr := &http.Transport{TLSClientConfig: tlsConfig}
		t.Cleanup(tr.CloseIdleConnections)
		f.http = &http.Client{Transport: tr, Timeout: 5 * time.Second}
		f.address = "https://" + address
	}
	if address := addresses["gRPC listening"]; address != "" {
		c, err := grpc.NewClient(address, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		f.grpc = v1.NewRoutingServiceClient(c)
	}
	if config.Communications.NATS != nil {
		c, err := nats.Connect(broker, nats.Secure(tlsConfig), nats.TLSHandshakeFirst(), nats.NoReconnect())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(c.Close)
		f.nats = c
	}
	nativeConfig := &actualAPIs[0]
	connection, err := grpc.NewClient("passthrough:///"+nativeConfig.Address, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, nativeConfig.Address)
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { connection.Close() })
	f.native = native.NewRoutingServiceClient(connection)
	f.nativeConn = connection
	for _, api := range actualAPIs {
		awaitNative(t, api.Network, api.Address)
	}
	return f
}
func awaitNative(t *testing.T, network, address string) {
	t.Helper()
	c, err := grpc.NewClient("passthrough:///"+address, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		call, stop := context.WithTimeout(ctx, 200*time.Millisecond)
		_, err := stats.NewStatsServiceClient(c).GetSysStats(call, &stats.SysStatsRequest{})
		stop()
		if err == nil {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("native not ready", err)
		case <-ticker.C:
		}
	}
}
func (f *fixture) traffic(t *testing.T, want bool) {
	t.Helper()
	proxy, _ := url.Parse("http://" + f.proxy)
	tr := &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 600 * time.Millisecond}
	response, err := client.Get(f.target.URL)
	ok := false
	if response != nil {
		data, _ := io.ReadAll(response.Body)
		response.Body.Close()
		ok = err == nil && string(data) == "routing-payload"
	}
	if ok != want {
		t.Fatalf("actual network traffic=%v want=%v error=%v", ok, want, err)
	}
}
func (f *fixture) invoke(t *testing.T, transport, op string, input map[string]any) map[string]any {
	t.Helper()
	if input == nil {
		input = map[string]any{}
	}
	id := "edge-a"
	if value, ok := input["instance_id"].(string); ok {
		id = value
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var data []byte
	switch transport {
	case "http":
		path, method := "", http.MethodGet
		query := url.Values{}
		switch op {
		case "list":
			path = "rules"
			for _, key := range []string{"page_size", "page_token"} {
				if v, ok := input[key]; ok {
					query.Set(key, fmt.Sprint(v))
				}
			}
		case "add":
			path = "rules"
			method = http.MethodPost
		case "remove":
			path = "rules"
			method = http.MethodDelete
			query.Set("tag", fmt.Sprint(input["tag"]))
		case "probe":
			path = "tests"
			method = http.MethodPost
		case "balancer":
			path = "balancers"
			query.Set("tag", fmt.Sprint(input["tag"]))
		case "override":
			path = "balancers/override"
			method = http.MethodPut
		case "status":
			path = "control-status"
		}
		var body io.Reader
		if method == http.MethodPost || method == http.MethodPut {
			values := map[string]any{}
			for k, v := range input {
				if k != "instance_id" {
					values[k] = v
				}
			}
			encoded, _ := json.Marshal(values)
			body = bytes.NewReader(encoded)
		}
		request, _ := http.NewRequestWithContext(ctx, method, f.address+"/v1/xray/network/instances/"+id+"/routing/"+path+"?"+query.Encode(), body)
		if body != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		response, err := f.http.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err = io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
	case "nats":
		values := map[string]any{"instance_id": id}
		for k, v := range input {
			values[k] = v
		}
		encoded, _ := json.Marshal(values)
		suffix := map[string]string{"list": "rules.v1.list", "add": "rules.v1.add", "remove": "rules.v1.remove", "probe": "v1.test", "balancer": "balancers.v1.get", "override": "balancers.v1.override", "status": "status.v1.get"}[op]
		response, err := f.nats.RequestWithContext(ctx, "fixture.routing.xray.network.routing."+suffix, encoded)
		if err != nil {
			t.Fatal(err)
		}
		data = response.Data
	case "grpc":
		var response proto.Message
		var err error
		switch op {
		case "list":
			r := &v1.ListRoutingRulesRequest{InstanceId: id}
			if v, ok := input["page_size"].(int); ok {
				r.PageSize = uint32(v)
			}
			r.PageToken, _ = input["page_token"].(string)
			response, err = f.grpc.ListRoutingRules(ctx, r)
		case "add":
			mode := v1.RuleMode_RULE_MODE_UNSPECIFIED
			if input["mode"] == "append" {
				mode = v1.RuleMode_RULE_MODE_APPEND
			}
			if input["mode"] == "replace" {
				mode = v1.RuleMode_RULE_MODE_REPLACE
			}
			rule, _ := json.Marshal(input["rule_json"])
			confirm, _ := input["confirm_destructive"].(bool)
			response, err = f.grpc.AddRoutingRule(ctx, &v1.AddRoutingRuleRequest{InstanceId: id, RuleJson: rule, Mode: mode, ConfirmDestructive: confirm})
		case "remove":
			tag, _ := input["tag"].(string)
			response, err = f.grpc.RemoveRoutingRule(ctx, &v1.RemoveRoutingRuleRequest{InstanceId: id, RuleTag: tag})
		case "probe":
			r := &v1.TestRouteRequest{InstanceId: id, Context: &v1.RoutingContext{}}
			if v, ok := input["context"]; ok {
				encoded, _ := json.Marshal(v)
				if err := protojson.Unmarshal(encoded, r.Context); err != nil {
					t.Fatal(err)
				}
			}
			r.FieldSelectors, _ = input["field_selectors"].([]string)
			r.PublishResult, _ = input["publish_result"].(bool)
			response, err = f.grpc.TestRoute(ctx, r)
		case "balancer":
			tag, _ := input["tag"].(string)
			response, err = f.grpc.GetBalancerInfo(ctx, &v1.GetBalancerInfoRequest{InstanceId: id, BalancerTag: tag})
		case "override":
			tag, _ := input["balancer_tag"].(string)
			var target *string
			if value, ok := input["target"].(string); ok {
				target = &value
			}
			response, err = f.grpc.OverrideBalancerTarget(ctx, &v1.OverrideBalancerTargetRequest{InstanceId: id, BalancerTag: tag, Target: target})
		case "status":
			response, err = f.grpc.GetRoutingControlStatus(ctx, &v1.InstanceRequest{InstanceId: id})
		}
		options := protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}
		if err != nil {
			result := map[string]any{"error": status.Convert(err).Message()}
			for _, detail := range status.Convert(err).Details() {
				if o, ok := detail.(*v1.RoutingOperation); ok {
					encoded, _ := options.Marshal(o)
					var value map[string]any
					json.Unmarshal(encoded, &value)
					result["operation"] = value
				}
			}
			return result
		}
		data, err = options.Marshal(response)
		if err != nil {
			t.Fatal(err)
		}
		if op == "add" || op == "remove" || op == "override" {
			var value map[string]any
			json.Unmarshal(data, &value)
			return map[string]any{"operation": value}
		}
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("decode %s: %s: %v", op, data, err)
	}
	return result
}
func requireSuccess(t *testing.T, result map[string]any) {
	t.Helper()
	if e := result["error"]; e != nil && e != "" {
		t.Fatal("operation failed", result)
	}
}
func route(t *testing.T, f *fixture, transport, inbound string) string {
	t.Helper()
	r := f.invoke(t, transport, "probe", map[string]any{"context": map[string]any{"inbound_tag": inbound}, "field_selectors": []string{"outbound"}})
	requireSuccess(t, r)
	if r["traffic_verified"] != false {
		t.Fatal("selection claimed traffic", r)
	}
	return r["route"].(map[string]any)["outbound_tag"].(string)
}

// openTunnel keeps one real CONNECT flow alive across routing mutations.
func (f *fixture) openTunnel(t *testing.T) func() {
	t.Helper()
	connection, err := net.DialTimeout("tcp", f.proxy, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { connection.Close() })
	reader := bufio.NewReader(connection)
	target, _ := url.Parse(f.target.URL)
	connection.SetDeadline(time.Now().Add(2 * time.Second))
	fmt.Fprintf(connection, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target.Host, target.Host)
	response, err := http.ReadResponse(reader, &http.Request{Method: "CONNECT"})
	if err != nil || response.StatusCode != 200 {
		t.Fatal("CONNECT failed", response, err)
	}
	return func() {
		t.Helper()
		connection.SetDeadline(time.Now().Add(2 * time.Second))
		if _, err := fmt.Fprintf(connection, "GET / HTTP/1.1\r\nHost: %s\r\n\r\n", target.Host); err != nil {
			t.Fatal(err)
		}
		response, err := http.ReadResponse(reader, &http.Request{Method: "GET"})
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || string(data) != "routing-payload" {
			t.Fatal("established flow stopped", string(data), err)
		}
	}
}
