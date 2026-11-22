//go:build integration && linux

package integration_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
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
type fixture struct {
	config settings.Config
	pki    testutil.PKI
	native string
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
	native := listener.Addr().String()
	accountListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	accountPort := accountListener.Addr().(*net.TCPAddr).Port
	defer accountListener.Close()
	source := filepath.Join(t.TempDir(), "config.json")
	data, err := json.Marshal(map[string]any{
		"log": map[string]any{"loglevel": "none"},
		"api": map[string]any{"tag": "api", "listen": native, "services": []string{"StatsService", "HandlerService"}},
		"inbounds": []any{map[string]any{
			"tag": "members", "listen": "127.0.0.1", "port": accountPort, "protocol": "vless",
			"settings": map[string]any{
				"decryption": "none",
				"clients": []any{
					map[string]any{"email": "first@example.test", "id": "a122228b-546c-4887-9638-08a744fb9074"},
					map[string]any{"email": "second@example.test", "id": "954395fb-84cf-4f1b-a5fa-665c13186121"},
				},
			},
		}},
		"stats":     map[string]any{},
		"outbounds": []any{map[string]any{"tag": "baseline", "protocol": "blackhole"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(source, data, 0600); err != nil {
		t.Fatal(err)
	}
	listener.Close()
	config := settings.Config{StartupTimeout: 4 * time.Second, ShutdownTimeout: 6 * time.Second, Instances: []settings.Instance{{ID: "edge-a", Engine: "xray", StopTimeout: time.Second, Xray: &settings.Xray{Binary: binary, Configuration: source, API: &settings.XrayAPI{Network: "tcp", Address: native}}}}}
	config.Communications.Requests.Timeout = 500 * time.Millisecond
	if transport == "http" || transport == "all" {
		config.Communications.HTTP = &settings.HTTP{Address: "127.0.0.1:0", TLS: pki.Server, ReadTimeout: time.Second, WriteTimeout: 2 * time.Second, IdleTimeout: time.Second, ShutdownTimeout: time.Second, BodyLimit: 8192, Concurrency: 16}
	}
	if transport == "grpc" || transport == "all" {
		config.Communications.GRPC = &settings.GRPC{Address: "127.0.0.1:0", TLS: pki.Server, ConnectionTimeout: 100 * time.Millisecond, ShutdownTimeout: time.Second, MaxRecvMessageSize: 8192, MaxSendMessageSize: 8192, MaxConnections: 8, MaxConcurrentStreams: 8, MaxHeaderListSize: 8192}
	}
	if transport == "nats" || transport == "all" {
		config.Communications.NATS = &settings.NATS{Servers: []string{broker}, TLS: pki.Client, TLSHandshakeFirst: true, ConnectTimeout: time.Second, ShutdownTimeout: time.Second, WriteTimeout: time.Second, ReconnectWait: time.Second, MaxReconnects: 0, ReconnectBufferSize: -1, PingInterval: time.Second, MaxPingsOutstanding: 2}
		config.Communications.Requests.NATS = &settings.NATSRequests{SubjectPrefix: "fixture.accounts", PendingMessages: 8, PendingBytes: 8192, ShutdownTimeout: 10 * time.Millisecond}
	}
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
	return fixture{config, pki, native}
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

type accountFamily struct {
	name, proxy, account string
	caseSensitive        bool
	duplicate            string
	missingRemovalOK     bool
	settings             func(string, byte) map[string]any
}

func nativeAccountFamilies() []accountFamily {
	key := func(seed byte, size int) string {
		return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{seed}, size))
	}
	member := func(email string, fields map[string]any) map[string]any {
		fields["email"], fields["level"] = email, 7
		return fields
	}
	return []accountFamily{
		{name: "vless", proxy: "vless", account: "xray.proxy.vless.Account", duplicate: "rejected", settings: func(email string, seed byte) map[string]any {
			return map[string]any{"decryption": "none", "clients": []any{member(email, map[string]any{"id": fmt.Sprintf("00000000-0000-4000-8000-%012x", seed)})}}
		}},
		{name: "vmess", proxy: "vmess", account: "xray.proxy.vmess.Account", duplicate: "rejected", settings: func(email string, seed byte) map[string]any {
			return map[string]any{"clients": []any{member(email, map[string]any{"id": fmt.Sprintf("00000000-0000-4000-8000-%012x", seed)})}}
		}},
		{name: "trojan", proxy: "trojan", account: "xray.proxy.trojan.Account", duplicate: "rejected", settings: func(email string, seed byte) map[string]any {
			return map[string]any{"clients": []any{member(email, map[string]any{"password": fmt.Sprintf("fixture-%d", seed)})}}
		}},
		{name: "shadowsocks", proxy: "shadowsocks", account: "xray.proxy.shadowsocks.Account", duplicate: "appended", settings: func(email string, seed byte) map[string]any {
			return map[string]any{"clients": []any{member(email, map[string]any{"method": "aes-128-gcm", "password": fmt.Sprintf("fixture-%d", seed)})}}
		}},
		{name: "shadowsocks2022", proxy: "shadowsocks", account: "xray.proxy.shadowsocks_2022.Account", duplicate: "rejected", settings: func(email string, seed byte) map[string]any {
			return map[string]any{"method": "2022-blake3-aes-128-gcm", "password": key(1, 16), "clients": []any{member(email, map[string]any{"password": key(seed, 16)})}}
		}},
		{name: "wireguard", proxy: "wireguard", account: "xray.proxy.wireguard.PeerConfig", caseSensitive: true, duplicate: "replaced", missingRemovalOK: true, settings: func(email string, seed byte) map[string]any {
			return map[string]any{"secretKey": key(1, 32), "address": []string{"10.55.0.1/24"}, "peers": []any{member(email, map[string]any{"publicKey": key(seed, 32), "allowedIPs": []string{fmt.Sprintf("10.55.0.%d/32", seed)}})}}
		}},
		{name: "hysteria", proxy: "hysteria", account: "xray.proxy.hysteria.account.Account", caseSensitive: true, duplicate: "replaced", missingRemovalOK: true, settings: func(email string, seed byte) map[string]any {
			return map[string]any{"version": 2, "clients": []any{member(email, map[string]any{"auth": fmt.Sprintf("fixture-%d", seed)})}}
		}},
		{name: "masque", proxy: "masque", account: "xray.proxy.masque.Account", duplicate: "rejected", settings: func(email string, seed byte) map[string]any {
			return map[string]any{"address": []string{"10.56.0.1/24"}, "clients": []any{member(email, map[string]any{"pass": fmt.Sprintf("fixture-%d", seed)})}}
		}},
	}
}

func (f accountFamily) inbound(email string, seed byte) map[string]any {
	return map[string]any{"tag": "members", "port": 1, "protocol": f.proxy, "settings": f.settings(email, seed)}
}

func familySource(t *testing.T, family accountFamily, email string, seed byte) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]any{"inbounds": []any{family.inbound(email, seed)}})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func configureFamily(t *testing.T, f fixture, family accountFamily) {
	t.Helper()
	// MASQUE can bind both TCP and UDP; reserve both until its config is written.
	var tcp net.Listener
	var udp net.PacketConn
	var err error
	for range 8 {
		tcp, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		udp, err = net.ListenPacket("udp", tcp.Addr().String())
		if err == nil {
			break
		}
		tcp.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	defer tcp.Close()
	defer udp.Close()
	path := f.config.Instances[0].Xray.Configuration
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var configuration map[string]any
	if err := json.Unmarshal(data, &configuration); err != nil {
		t.Fatal(err)
	}
	inbound := family.inbound("original@example.test", 2)
	inbound["listen"], inbound["port"] = "127.0.0.1", tcp.Addr().(*net.TCPAddr).Port
	if family.name == "hysteria" || family.name == "masque" {
		inbound["streamSettings"] = map[string]any{
			"network": family.name, "security": "tls",
			"tlsSettings": map[string]any{"certificates": []any{map[string]any{"certificateFile": f.pki.Server.Certificate, "keyFile": f.pki.Server.PrivateKey}}},
		}
		if family.name == "hysteria" {
			inbound["streamSettings"].(map[string]any)["hysteriaSettings"] = map[string]any{"version": 2}
		}
	}
	configuration["inbounds"] = []any{inbound}
	data, err = json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}
