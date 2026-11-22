//go:build integration && linux

package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/domain"
	trafficv1 "github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/bootstrap"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/shared/mtls"
	"github.com/PapilioVPN/node/internal/testutil"
	"github.com/nats-io/nats.go"
	stats "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

const account = "User>>>fixture@example.test"
const inboundTag = "in>>>fixture"
const outboundTag = "out>>>fixture"

type listenerLog struct {
	slog.Handler
	mu        sync.Mutex
	addresses map[string]string
}

func (l *listenerLog) Handle(_ context.Context, record slog.Record) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	record.Attrs(func(a slog.Attr) bool {
		if a.Key == "address" {
			l.addresses[record.Message] = a.Value.String()
		}
		return true
	})
	return nil
}

type fixture struct {
	config   settings.Config
	proxy    string
	target   *httptest.Server
	original []byte
	api      stats.StatsServiceClient
	http     *http.Client
	address  string
	grpc     trafficv1.TrafficServiceClient
	nats     *nats.Conn
}

func newFixture(t *testing.T, pki testutil.PKI, broker, transport string) *fixture {
	t.Helper()
	binary := os.Getenv("XRAY_TEST_BINARY")
	if binary == "" {
		t.Fatal("XRAY_TEST_BINARY must select the verified executable")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	nativeReservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { nativeReservation.Close() })
	proxyReservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { proxyReservation.Close() })
	f := &fixture{proxy: proxyReservation.Addr().String()}
	_, port, _ := net.SplitHostPort(f.proxy)
	number, _ := strconv.Atoi(port)
	f.target = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		io.WriteString(w, strings.Repeat("counter-payload", 1024))
	}))
	t.Cleanup(f.target.Close)
	data, err := json.Marshal(map[string]any{
		"log": map[string]any{"loglevel": "none"}, "api": map[string]any{"tag": "api", "listen": nativeReservation.Addr().String(), "services": []string{"StatsService"}}, "stats": map[string]any{},
		"policy":    map[string]any{"levels": map[string]any{"0": map[string]any{"statsUserUplink": true, "statsUserDownlink": true}}, "system": map[string]any{"statsInboundUplink": true, "statsInboundDownlink": true, "statsOutboundUplink": true, "statsOutboundDownlink": true}},
		"inbounds":  []any{map[string]any{"tag": inboundTag, "listen": "127.0.0.1", "port": number, "protocol": "http", "settings": map[string]any{"accounts": []any{map[string]any{"user": account, "pass": "fixture-password"}}, "userLevel": 0}}},
		"outbounds": []any{map[string]any{"tag": outboundTag, "protocol": "freedom"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(source, data, 0600); err != nil {
		t.Fatal(err)
	}
	f.original = data
	f.config = settings.Config{StartupTimeout: 5 * time.Second, ShutdownTimeout: 6 * time.Second, Instances: []settings.Instance{{ID: "edge-a", Engine: "xray", StopTimeout: time.Second, Xray: &settings.Xray{Binary: binary, Configuration: source, API: &settings.XrayAPI{Network: "tcp", Address: nativeReservation.Addr().String()}}}}}
	f.config.Communications.Requests.Timeout = time.Second
	switch transport {
	case "http":
		f.config.Communications.HTTP = &settings.HTTP{Address: "127.0.0.1:0", TLS: pki.Server, ReadTimeout: time.Second, WriteTimeout: 2 * time.Second, IdleTimeout: time.Second, ShutdownTimeout: time.Second, BodyLimit: 32 << 10, Concurrency: 16}
	case "grpc":
		f.config.Communications.GRPC = &settings.GRPC{Address: "127.0.0.1:0", TLS: pki.Server, ConnectionTimeout: 100 * time.Millisecond, ShutdownTimeout: time.Second, MaxRecvMessageSize: 32 << 10, MaxSendMessageSize: 32 << 10, MaxConnections: 8, MaxConcurrentStreams: 8, MaxHeaderListSize: 8192}
	case "nats":
		f.config.Communications.NATS = &settings.NATS{Servers: []string{broker}, TLS: pki.Client, TLSHandshakeFirst: true, ConnectTimeout: time.Second, ShutdownTimeout: time.Second, WriteTimeout: time.Second, ReconnectWait: time.Second, MaxReconnects: 0, ReconnectBufferSize: -1, PingInterval: time.Second, MaxPingsOutstanding: 2}
		f.config.Communications.Requests.NATS = &settings.NATSRequests{SubjectPrefix: "fixture.traffic", PendingMessages: 8, PendingBytes: 32 << 10, ShutdownTimeout: 10 * time.Millisecond}
	}
	if err := f.config.Validate(); err != nil {
		t.Fatal(err)
	}
	log := &listenerLog{Handler: slog.NewTextHandler(io.Discard, nil), addresses: map[string]string{}}
	app := bootstrap.New(f.config, slog.New(log))
	nativeReservation.Close()
	proxyReservation.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		if err := app.Stop(ctx); err != nil {
			t.Error(err)
		}
	})
	connection, err := grpc.NewClient(f.config.Instances[0].Xray.API.Address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { connection.Close() })
	f.api = stats.NewStatsServiceClient(connection)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		_, err = f.api.GetSysStats(ctx, &stats.SysStatsRequest{})
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("native API not ready", err)
		case <-ticker.C:
		}
	}
	identity, err := mtls.Client(pki.Client)
	if err != nil {
		t.Fatal(err)
	}
	log.mu.Lock()
	defer log.mu.Unlock()
	switch transport {
	case "http":
		resource := &http.Transport{TLSClientConfig: identity}
		t.Cleanup(resource.CloseIdleConnections)
		f.http = &http.Client{Transport: resource, Timeout: 2 * time.Second}
		f.address = "https://" + log.addresses["HTTP listening"]
	case "grpc":
		connection, err := grpc.NewClient(log.addresses["gRPC listening"], grpc.WithTransportCredentials(credentials.NewTLS(identity)))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { connection.Close() })
		f.grpc = trafficv1.NewTrafficServiceClient(connection)
	case "nats":
		f.nats, err = nats.Connect(broker, nats.Secure(identity), nats.TLSHandshakeFirst(), nats.NoReconnect())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(f.nats.Close)
	}
	return f
}

func (f *fixture) sendTraffic(t *testing.T) {
	t.Helper()
	proxy, _ := url.Parse("http://" + f.proxy)
	proxy.User = url.UserPassword(account, "fixture-password")
	resource := &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}
	defer resource.CloseIdleConnections()
	client := &http.Client{Transport: resource, Timeout: time.Second}
	response, err := client.Post(f.target.URL, "text/plain", strings.NewReader(strings.Repeat("uplink", 1024)))
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || string(data) != strings.Repeat("counter-payload", 1024) {
		t.Fatal("native traffic failed", err, response.StatusCode)
	}
}

type observation struct {
	InstanceID string           `json:"instance_id"`
	Generation uint64           `json:"generation,string"`
	Scope      domain.Scope     `json:"scope"`
	Name       string           `json:"name"`
	Direction  domain.Direction `json:"direction"`
	Bytes      int64            `json:"bytes,string"`
	Source     string           `json:"source"`
	ObservedAt time.Time        `json:"observed_at"`
	DurationNS int64            `json:"duration_ns,string"`
	Error      string           `json:"error"`
}

func (f *fixture) read(t *testing.T, transport string, selection domain.Selection) observation {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var result observation
	switch transport {
	case "http":
		query := url.Values{"scope": {string(selection.Scope)}, "name": {selection.Name}, "direction": {string(selection.Direction)}}
		response, err := f.http.Get(f.address + "/v1/xray/observability/instances/edge-a/traffic/counter?" + query.Encode())
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != 200 && (response.StatusCode != 404 || result.Error != "counter_not_found") {
			t.Fatal("unexpected HTTP result", response.StatusCode, result)
		}
	case "grpc":
		scopes := map[domain.Scope]trafficv1.Scope{domain.User: trafficv1.Scope_SCOPE_USER, domain.Inbound: trafficv1.Scope_SCOPE_INBOUND, domain.Outbound: trafficv1.Scope_SCOPE_OUTBOUND}
		directions := map[domain.Direction]trafficv1.Direction{domain.Uplink: trafficv1.Direction_DIRECTION_UPLINK, domain.Downlink: trafficv1.Direction_DIRECTION_DOWNLINK}
		response, err := f.grpc.GetTrafficCounter(ctx, &trafficv1.GetTrafficCounterRequest{InstanceId: "edge-a", Scope: scopes[selection.Scope], Name: selection.Name, Direction: directions[selection.Direction]})
		if err != nil {
			if status.Code(err) != codes.NotFound || status.Convert(err).Message() != "counter_not_found" {
				t.Fatal(err)
			}
			return observation{Error: "counter_not_found"}
		}
		returnedScopes := map[trafficv1.Scope]domain.Scope{trafficv1.Scope_SCOPE_USER: domain.User, trafficv1.Scope_SCOPE_INBOUND: domain.Inbound, trafficv1.Scope_SCOPE_OUTBOUND: domain.Outbound}
		returnedDirections := map[trafficv1.Direction]domain.Direction{trafficv1.Direction_DIRECTION_UPLINK: domain.Uplink, trafficv1.Direction_DIRECTION_DOWNLINK: domain.Downlink}
		result = observation{InstanceID: response.InstanceId, Generation: response.Generation, Scope: returnedScopes[response.Scope], Name: response.Name, Direction: returnedDirections[response.Direction], Bytes: response.Bytes, Source: response.Source, ObservedAt: response.ObservedAt.AsTime(), DurationNS: response.DurationNs}
	case "nats":
		data, _ := json.Marshal(map[string]string{"instance_id": "edge-a", "scope": string(selection.Scope), "name": selection.Name, "direction": string(selection.Direction)})
		message, err := f.nats.RequestWithContext(ctx, "fixture.traffic.xray.observability.traffic.v1.get", data)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(message.Data, &result); err != nil {
			t.Fatal(err)
		}
	}
	return result
}
