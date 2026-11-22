//go:build integration && linux

package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/bootstrap"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/shared/mtls"
	"github.com/PapilioVPN/node/internal/testutil"
	"github.com/nats-io/nats.go"
	stats "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

type listenerLog struct {
	slog.Handler
	mu        sync.Mutex
	addresses map[string]string
}

func (l *listenerLog) Handle(_ context.Context, r slog.Record) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "address" {
			l.addresses[r.Message] = a.Value.String()
		}
		return true
	})
	return nil
}

type clients struct {
	http    *http.Client
	address string
	grpc    *grpc.ClientConn
	nats    *nats.Conn
}
type fixtureResources struct{ listeners []net.Listener }

func (r *fixtureResources) release() {
	for _, listener := range r.listeners {
		listener.Close()
	}
	r.listeners = nil
}

type fixture struct {
	resources *fixtureResources
	config    settings.Config
	pki       testutil.PKI
	native    string
}

func newFixture(t *testing.T, pki testutil.PKI, broker, transport string) fixture {
	t.Helper()
	binary := os.Getenv("XRAY_TEST_BINARY")
	if binary == "" {
		t.Fatal("XRAY_TEST_BINARY must select the verified executable")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	resources := &fixtureResources{listeners: []net.Listener{listener}}
	t.Cleanup(resources.release)
	native := listener.Addr().String()
	source := filepath.Join(t.TempDir(), "config.json")
	data, _ := json.Marshal(map[string]any{"log": map[string]any{"loglevel": "none"}, "api": map[string]any{"tag": "api", "listen": native, "services": []string{"StatsService", "HandlerService"}}, "stats": map[string]any{}, "outbounds": []any{map[string]any{"tag": "baseline", "protocol": "blackhole"}}})
	if err := os.WriteFile(source, data, 0600); err != nil {
		t.Fatal(err)
	}
	config := settings.Config{StartupTimeout: 4 * time.Second, ShutdownTimeout: 12 * time.Second, Instances: []settings.Instance{{ID: "edge-a", Engine: "xray", StopTimeout: time.Second, Xray: &settings.Xray{Binary: binary, Configuration: source, API: &settings.XrayAPI{Network: "tcp", Address: native}}}}}
	config.Communications.Requests.Timeout = 500 * time.Millisecond
	if transport == "http" || transport == "all" {
		config.Communications.HTTP = &settings.HTTP{Address: "127.0.0.1:0", TLS: pki.Server, ReadTimeout: time.Second, WriteTimeout: 2 * time.Second, IdleTimeout: time.Second, ShutdownTimeout: time.Second, BodyLimit: 8192, Concurrency: 16}
	}
	if transport == "grpc" || transport == "all" {
		config.Communications.GRPC = &settings.GRPC{Address: "127.0.0.1:0", TLS: pki.Server, ConnectionTimeout: 100 * time.Millisecond, ShutdownTimeout: time.Second, MaxRecvMessageSize: 8192, MaxSendMessageSize: 8192, MaxConnections: 8, MaxConcurrentStreams: 8, MaxHeaderListSize: 8192}
	}
	if transport == "nats" || transport == "all" {
		config.Communications.NATS = &settings.NATS{Servers: []string{broker}, TLS: pki.Client, TLSHandshakeFirst: true, ConnectTimeout: time.Second, ShutdownTimeout: time.Second, WriteTimeout: time.Second, ReconnectWait: time.Second, MaxReconnects: 0, ReconnectBufferSize: -1, PingInterval: time.Second, MaxPingsOutstanding: 2}
		config.Communications.Requests.NATS = &settings.NATSRequests{SubjectPrefix: "fixture.outbounds", PendingMessages: 8, PendingBytes: 8192, ShutdownTimeout: 50 * time.Millisecond}
	}
	// Reserve fixed management addresses before releasing pending child binds.
	for _, transport := range []string{"http", "grpc"} {
		if (transport == "http" && config.Communications.HTTP == nil) || (transport == "grpc" && config.Communications.GRPC == nil) {
			continue
		}
		management, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		resources.listeners = append(resources.listeners, management)
		if transport == "http" {
			config.Communications.HTTP.Address = management.Addr().String()
		} else {
			config.Communications.GRPC.Address = management.Addr().String()
		}
	}
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
	return fixture{config: config, pki: pki, native: native, resources: resources}
}
func startNode(t *testing.T, f fixture) clients {
	t.Helper()
	log := &listenerLog{Handler: slog.NewTextHandler(io.Discard, nil), addresses: map[string]string{}}
	app := bootstrap.New(f.config, slog.New(log))
	ctx, cancel := context.WithTimeout(context.Background(), f.config.StartupTimeout)
	defer cancel()
	f.resources.release()
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), f.config.ShutdownTimeout)
		defer cancel()
		if err := app.Stop(ctx); err != nil {
			t.Error(err)
		}
	})
	log.mu.Lock()
	defer log.mu.Unlock()
	return connect(t, f, log.addresses)
}
func connect(t *testing.T, f fixture, addresses map[string]string) clients {
	t.Helper()
	tlsConfig, err := mtls.Client(f.pki.Client)
	if err != nil {
		t.Fatal(err)
	}
	var result clients
	if address := addresses["HTTP listening"]; address != "" {
		transport := &http.Transport{TLSClientConfig: tlsConfig}
		t.Cleanup(transport.CloseIdleConnections)
		result.http = &http.Client{Transport: transport, Timeout: 2 * time.Second}
		result.address = "https://" + address
	}
	if address := addresses["gRPC listening"]; address != "" {
		result.grpc, err = grpc.NewClient(address, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { result.grpc.Close() })
	}
	if config := f.config.Communications.NATS; config != nil {
		result.nats, err = nats.Connect(config.Servers[0], nats.Secure(tlsConfig), nats.TLSHandshakeFirst(), nats.NoReconnect())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(result.nats.Close)
	}
	return result
}
func awaitNative(t *testing.T, address string) {
	t.Helper()
	connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		probe, stop := context.WithTimeout(ctx, 100*time.Millisecond)
		_, err := stats.NewStatsServiceClient(connection).GetSysStats(probe, &stats.SysStatsRequest{})
		stop()
		if err == nil {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("native Xray statistics not ready: %v", err)
		case <-ticker.C:
		}
	}
}
func brokerFixture(t *testing.T, pki testutil.PKI) string {
	t.Helper()
	broker := testutil.NewNATSBroker(t, pki.Server, true)
	return strings.Replace(broker.ClientURL(), "nats://", "tls://", 1)
}

func configureProbe(t *testing.T, f fixture, route bool) string {
	t.Helper()
	reserved, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f.resources.listeners = append(f.resources.listeners, reserved)
	address := reserved.Addr().String()
	port := reserved.Addr().(*net.TCPAddr).Port
	path := f.config.Instances[0].Xray.Configuration
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var source map[string]any
	if err := json.Unmarshal(data, &source); err != nil {
		t.Fatal(err)
	}
	source["inbounds"] = []any{map[string]any{"tag": "probe", "listen": "127.0.0.1", "port": port, "protocol": "http"}}
	if route {
		source["routing"] = map[string]any{"rules": []any{map[string]any{"type": "field", "inboundTag": []string{"probe"}, "outboundTag": "managed"}}}
	} else {
		source["outbounds"] = []any{map[string]any{"tag": "baseline", "protocol": "freedom"}, map[string]any{"tag": "backup", "protocol": "freedom"}}
	}
	data, _ = json.Marshal(source)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return address
}
