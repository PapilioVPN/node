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

	"github.com/PapilioVPN/node/internal/bootstrap"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/shared/mtls"
	"github.com/PapilioVPN/node/internal/testutil"
	"github.com/nats-io/nats.go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

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
	config           settings.Config
	pki              testutil.PKI
	directory, proxy string
	target           *httptest.Server
	original         []byte
}
type clients struct {
	http    *http.Client
	address string
	grpc    *grpc.ClientConn
	nats    *nats.Conn
}

const subject = "fixture.configs.xray.runtime.configs.v1.validate"

func nodeFixture(t *testing.T, pki testutil.PKI, brokerURL, transport string) fixture {
	t.Helper()
	binary, err := filepath.Abs(os.Getenv("XRAY_TEST_BINARY"))
	if err != nil || os.Getenv("XRAY_TEST_BINARY") == "" {
		t.Fatal("XRAY_TEST_BINARY must select the verified executable")
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "unchanged-engine-traffic") }))
	t.Cleanup(target.Close)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	proxy := listener.Addr().String()
	_, port, _ := net.SplitHostPort(proxy)
	numericPort, _ := strconv.Atoi(port)
	directory := t.TempDir()
	path := filepath.Join(directory, "active.json")
	work := filepath.Join(directory, "work")
	if err := os.Mkdir(work, 0700); err != nil {
		t.Fatal(err)
	}
	original, err := json.Marshal(map[string]any{"log": map[string]any{"loglevel": "none"}, "inbounds": []any{map[string]any{"listen": "127.0.0.1", "port": numericPort, "protocol": "http", "settings": map[string]any{}}}, "outbounds": []any{map[string]any{"protocol": "freedom"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	listener.Close()
	config := settings.Config{StartupTimeout: 5 * time.Second, ShutdownTimeout: 10 * time.Second, Instances: []settings.Instance{{ID: "edge-a", Engine: "xray", StopTimeout: time.Second, Xray: &settings.Xray{Binary: binary, Configuration: path, WorkDirectory: work}}}}
	config.Communications.Requests.Timeout = time.Second
	if transport == "http" || transport == "all" {
		config.Communications.HTTP = &settings.HTTP{Address: "127.0.0.1:0", TLS: pki.Server, ReadTimeout: 2 * time.Second, WriteTimeout: 3 * time.Second, IdleTimeout: time.Second, ShutdownTimeout: time.Second, BodyLimit: 2 << 20, Concurrency: 16}
	}
	if transport == "grpc" || transport == "all" {
		config.Communications.GRPC = &settings.GRPC{Address: "127.0.0.1:0", TLS: pki.Server, ConnectionTimeout: 100 * time.Millisecond, ShutdownTimeout: time.Second, MaxRecvMessageSize: 2 << 20, MaxSendMessageSize: 8192, MaxConnections: 16, MaxConcurrentStreams: 8, MaxHeaderListSize: 8192}
	}
	if transport == "nats" || transport == "all" {
		config.Communications.NATS = &settings.NATS{Servers: []string{brokerURL}, TLS: pki.Client, TLSHandshakeFirst: true, ConnectTimeout: time.Second, ShutdownTimeout: time.Second, WriteTimeout: time.Second, ReconnectWait: time.Second, MaxReconnects: 0, ReconnectBufferSize: -1, PingInterval: time.Second, MaxPingsOutstanding: 2}
		config.Communications.Requests.NATS = &settings.NATSRequests{SubjectPrefix: "fixture.configs", PendingMessages: 8, PendingBytes: 2 << 20, ShutdownTimeout: 50 * time.Millisecond}
	}
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
	return fixture{config, pki, directory, proxy, target, original}
}

func startNode(t *testing.T, f fixture) clients {
	t.Helper()
	log := &listenerLog{Handler: slog.NewTextHandler(io.Discard, nil), addresses: map[string]string{}}
	app := bootstrap.New(f.config, slog.New(log))
	ctx, cancel := context.WithTimeout(context.Background(), f.config.StartupTimeout)
	defer cancel()
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
	return connect(t, f.pki.Client, f.config, log.addresses)
}

func connect(t *testing.T, files mtls.Files, config settings.Config, addresses map[string]string) clients {
	t.Helper()
	identity, err := mtls.Client(files)
	if err != nil {
		t.Fatal(err)
	}
	var result clients
	if address := addresses["HTTP listening"]; address != "" {
		transport := &http.Transport{TLSClientConfig: identity}
		t.Cleanup(transport.CloseIdleConnections)
		result.http, result.address = &http.Client{Transport: transport, Timeout: 3 * time.Second}, "https://"+address
	}
	if address := addresses["gRPC listening"]; address != "" {
		result.grpc, err = grpc.NewClient(address, grpc.WithTransportCredentials(credentials.NewTLS(identity)))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { result.grpc.Close() })
	}
	if config.Communications.NATS != nil {
		result.nats, err = nats.Connect(config.Communications.NATS.Servers[0], nats.Secure(identity), nats.TLSHandshakeFirst(), nats.NoReconnect(), nats.Timeout(time.Second))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(result.nats.Close)
	}
	return result
}

func brokerFixture(t *testing.T, pki testutil.PKI) string {
	t.Helper()
	broker := testutil.NewNATSBroker(t, pki.Server, true)
	return strings.Replace(broker.ClientURL(), "nats://", "tls://", 1)
}

func traffic(proxy, target string) error {
	endpoint, _ := url.Parse("http://" + proxy)
	transport := &http.Transport{Proxy: http.ProxyURL(endpoint), DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	response, err := (&http.Client{Transport: transport, Timeout: 200 * time.Millisecond}).Get(target)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err == nil && string(data) != "unchanged-engine-traffic" {
		return io.ErrUnexpectedEOF
	}
	return err
}
func awaitTraffic(t *testing.T, f fixture) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if traffic(f.proxy, f.target.URL) == nil {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("real Xray traffic unavailable")
		case <-tick.C:
		}
	}
}
