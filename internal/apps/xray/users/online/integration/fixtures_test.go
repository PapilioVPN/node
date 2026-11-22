//go:build integration && linux

package integration_test

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	instanceServices "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/process"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/rpc"
	"github.com/PapilioVPN/node/internal/apps/xray/users/online"
	"github.com/PapilioVPN/node/internal/apps/xray/users/online/app/services"
	onlinev1 "github.com/PapilioVPN/node/internal/apps/xray/users/online/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/platform/httpserver"
	"github.com/PapilioVPN/node/internal/platform/natsclient"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/shared/mtls"
	"github.com/PapilioVPN/node/internal/shared/shutdown"
	"github.com/PapilioVPN/node/internal/testutil"
	"github.com/nats-io/nats.go"
	stats "github.com/xtls/xray-core/app/stats/command"
	"go.uber.org/fx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

const email = "shared@example.test"

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

type fixture struct {
	transport         string
	proxies, disabled map[string]string
	lifecycle         *instanceServices.Service
	http              *http.Client
	address           string
	grpc              *grpc.ClientConn
	nats              *nats.Conn
	sources           map[string][]byte
	paths             map[string]string
}
type response struct {
	Observation struct {
		InstanceID string    `json:"instance_id"`
		Email      string    `json:"email"`
		Generation string    `json:"generation"`
		Source     string    `json:"source"`
		ObservedAt time.Time `json:"observed_at"`
	} `json:"observation"`
	State string  `json:"state"`
	Count *string `json:"count"`
	IPs   []struct {
		IP       string `json:"ip"`
		LastSeen int64  `json:"last_seen_unix_seconds,string"`
	} `json:"ips"`
	Error string `json:"error"`
}

// This fixture explicitly supplies the pending shared registration/StatusReader
// integration. No normal-entrypoint installation is claimed by these tests.
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
	dir, err := os.MkdirTemp("/tmp", "node-online-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	f := &fixture{transport: transport, proxies: map[string]string{}, disabled: map[string]string{}, sources: map[string][]byte{}, paths: map[string]string{}}
	bindings := []instanceServices.Binding{}
	nativeBindings := []rpc.Binding{}
	for _, id := range []string{"a", "b"} {
		apiListener := listen(t)
		proxyListener := listen(t)
		disabledListener := listen(t)
		address := apiListener.Addr().String()
		network := "tcp"
		nativeListen := address
		if id == "b" {
			network = "unix"
			address = filepath.Join(dir, "b.sock")
			nativeListen = address
		}
		f.proxies[id] = proxyListener.Addr().String()
		f.disabled[id] = disabledListener.Addr().String()
		inbound := func(tag, account string, port int, level int) any {
			return map[string]any{"tag": tag, "listen": "127.0.0.1", "port": port, "protocol": "http", "settings": map[string]any{"userLevel": level, "accounts": []any{map[string]any{"user": account, "pass": "fixture"}}}}
		}
		data, err := json.Marshal(map[string]any{
			"log": map[string]any{"loglevel": "none"}, "api": map[string]any{"tag": "api", "listen": nativeListen, "services": []string{"StatsService"}}, "stats": map[string]any{},
			"policy":    map[string]any{"levels": map[string]any{"0": map[string]any{"statsUserOnline": true}, "1": map[string]any{"statsUserOnline": false}}},
			"inbounds":  []any{inbound("tracked", email, proxyListener.Addr().(*net.TCPAddr).Port, 0), inbound("disabled", "disabled@example.test", disabledListener.Addr().(*net.TCPAddr).Port, 1)},
			"outbounds": []any{map[string]any{"protocol": "freedom", "settings": map[string]any{"allowedIPs": []string{"127.0.0.1/32"}}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, id+".json")
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		f.paths[id] = path
		f.sources[id] = data
		client, err := rpc.New(rpc.Config{Network: network, Address: address})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { client.Close() })
		runtime, err := process.New(process.Config{Binary: binary, Configuration: path, StopTimeout: time.Second, API: client})
		if err != nil {
			t.Fatal(err)
		}
		bindings = append(bindings, instanceServices.Binding{ID: id, Engine: "xray", Runtime: runtime})
		nativeBindings = append(nativeBindings, rpc.Binding{ID: id, Client: client})
		apiListener.Close()
		proxyListener.Close()
		disabledListener.Close()
	}
	f.lifecycle, err = instanceServices.New(bindings)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := f.lifecycle.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if err := f.lifecycle.StartAll(ctx); err != nil {
		t.Fatal(err)
	}
	for _, b := range nativeBindings {
		awaitNative(t, b.Client)
	}
	nativeBindings = append(nativeBindings, rpc.Binding{ID: "not-configured"})
	log := &listenerLog{Handler: slog.NewTextHandler(io.Discard, nil), addresses: map[string]string{}}
	requests := settings.Requests{Timeout: time.Second}
	options := []fx.Option{fx.NopLogger, fx.Supply(slog.New(log)), fx.Decorate(shutdown.Synchronize), fx.Provide(func() services.StatusReader { return f.lifecycle }), fx.Provide(fx.Annotate(func() []rpc.Binding { return nativeBindings }, fx.ResultTags(`group:"xray-native-api,flatten"`)))}
	switch transport {
	case "http":
		options = append(options, fx.Supply(settings.HTTP{Address: "127.0.0.1:0", TLS: pki.Server, ReadTimeout: time.Second, WriteTimeout: 2 * time.Second, IdleTimeout: time.Second, ShutdownTimeout: time.Second, BodyLimit: 32768, Concurrency: 16}), httpserver.Module)
	case "grpc":
		options = append(options, fx.Supply(settings.GRPC{Address: "127.0.0.1:0", TLS: pki.Server, ConnectionTimeout: 100 * time.Millisecond, ShutdownTimeout: time.Second, MaxRecvMessageSize: 32768, MaxSendMessageSize: 65536, MaxConnections: 8, MaxConcurrentStreams: 8, MaxHeaderListSize: 8192}), grpcserver.Module)
	case "nats":
		options = append(options, fx.Supply(settings.NATS{Servers: []string{broker}, TLS: pki.Client, TLSHandshakeFirst: true, ConnectTimeout: time.Second, ShutdownTimeout: time.Second, WriteTimeout: time.Second, ReconnectWait: time.Second, MaxReconnects: 0, ReconnectBufferSize: -1, PingInterval: time.Second, MaxPingsOutstanding: 2}), natsclient.Module, natsclient.RequestsModule(settings.NATSRequests{SubjectPrefix: "fixture.online", PendingMessages: 8, PendingBytes: 32768, ShutdownTimeout: 10 * time.Millisecond}, time.Second))
	}
	options = append(options, online.Module(requests))
	app := fx.New(options...)
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if err := app.Stop(ctx); err != nil {
			t.Error(err)
		}
	})
	tlsConfig, err := mtls.Client(pki.Client)
	if err != nil {
		t.Fatal(err)
	}
	log.mu.Lock()
	defer log.mu.Unlock()
	switch transport {
	case "http":
		tr := &http.Transport{TLSClientConfig: tlsConfig}
		f.http = &http.Client{Transport: tr, Timeout: 2 * time.Second}
		f.address = "https://" + log.addresses["HTTP listening"]
		t.Cleanup(tr.CloseIdleConnections)
		unauthorized := tlsConfig.Clone()
		unauthorized.Certificates = nil
		denied := &http.Transport{TLSClientConfig: unauthorized}
		defer denied.CloseIdleConnections()
		if r, err := (&http.Client{Transport: denied, Timeout: time.Second}).Get(f.address + "/v1/xray/users/instances/a/online?email=x"); err == nil {
			r.Body.Close()
			t.Fatal("HTTP accepted unauthenticated controller")
		}
	case "grpc":
		address := log.addresses["gRPC listening"]
		f.grpc, err = grpc.NewClient(address, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { f.grpc.Close() })
		unauthorized := tlsConfig.Clone()
		unauthorized.Certificates = nil
		denied, err := grpc.NewClient(address, grpc.WithTransportCredentials(credentials.NewTLS(unauthorized)))
		if err != nil {
			t.Fatal(err)
		}
		defer denied.Close()
		probe, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		if _, err := onlinev1.NewOnlineServiceClient(denied).GetUserOnlineStats(probe, &onlinev1.GetUserOnlineStatsRequest{InstanceId: "a", Email: email}); err == nil {
			t.Fatal("gRPC accepted unauthenticated controller")
		}
	case "nats":
		f.nats, err = nats.Connect(broker, nats.Secure(tlsConfig), nats.TLSHandshakeFirst(), nats.NoReconnect())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(f.nats.Close)
	}
	return f
}
func listen(t *testing.T) net.Listener {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l
}
func awaitNative(t *testing.T, connection *rpc.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ticker := time.NewTicker(15 * time.Millisecond)
	defer ticker.Stop()
	for {
		probe, stop := context.WithTimeout(ctx, 100*time.Millisecond)
		_, err := stats.NewStatsServiceClient(connection).GetSysStats(probe, &stats.SysStatsRequest{})
		stop()
		if err == nil {
			return
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("native not ready", err)
		}
	}
}

func (f *fixture) read(t *testing.T, id, account string, ips bool) response {
	t.Helper()
	var result response
	switch f.transport {
	case "http":
		path := "/online"
		if ips {
			path += "/ips"
		}
		r, err := f.http.Get(f.address + "/v1/xray/users/instances/" + id + path + "?email=" + url.QueryEscape(account))
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		if r.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("cacheable observation")
		}
		if err := json.NewDecoder(r.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
	case "grpc":
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		client := onlinev1.NewOnlineServiceClient(f.grpc)
		var o *onlinev1.OnlineObservation
		var state onlinev1.OnlineState
		if ips {
			r, err := client.ListUserOnlineIPs(ctx, &onlinev1.ListUserOnlineIPsRequest{InstanceId: id, Email: account})
			if err != nil {
				return response{Error: grpcMessage(err)}
			}
			o = r.Observation
			state = r.State
			for _, row := range r.Ips {
				result.IPs = append(result.IPs, struct {
					IP       string `json:"ip"`
					LastSeen int64  `json:"last_seen_unix_seconds,string"`
				}{row.Ip, row.LastSeenUnixSeconds})
			}
		} else {
			r, err := client.GetUserOnlineStats(ctx, &onlinev1.GetUserOnlineStatsRequest{InstanceId: id, Email: account})
			if err != nil {
				return response{Error: grpcMessage(err)}
			}
			o = r.Observation
			state = r.State
			if r.Count != nil {
				v := fmt.Sprint(*r.Count)
				result.Count = &v
			}
		}
		result.Observation.InstanceID = o.InstanceId
		result.Observation.Email = o.Email
		result.Observation.Generation = fmt.Sprint(o.Generation)
		result.Observation.Source = o.Source
		result.Observation.ObservedAt = o.ObservedAt.AsTime()
		switch state {
		case onlinev1.OnlineState_ONLINE_STATE_ONLINE:
			result.State = "online"
		case onlinev1.OnlineState_ONLINE_STATE_OFFLINE:
			result.State = "offline"
		case onlinev1.OnlineState_ONLINE_STATE_UNTRACKED:
			result.State = "untracked"
		}
	case "nats":
		subject := "fixture.online.xray.users.online.v1.get"
		if ips {
			subject = "fixture.online.xray.users.online.ips.v1.list"
		}
		data, _ := json.Marshal(map[string]string{"instance_id": id, "email": account})
		r, err := f.nats.Request(subject, data, 2*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(r.Data, &result); err != nil {
			t.Fatal(err)
		}
	}
	return result
}

func tunnel(t *testing.T, proxy, source, account, target string) net.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := (&net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP(source)}}).DialContext(ctx, "tcp", proxy)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	auth := base64.StdEncoding.EncodeToString([]byte(account + ":fixture"))
	if _, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: Basic %s\r\n\r\n", target, target, auth); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	first, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(first, "200") {
		t.Fatal(first, err)
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line == "\r\n" {
			break
		}
	}
	payload := "online-proof"
	if _, err := io.WriteString(conn, payload); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(reader, got); err != nil || string(got) != payload {
		t.Fatal("actual proxy traffic failed", string(got), err)
	}
	return conn
}

func echoTarget(t *testing.T) string {
	t.Helper()
	listener := listen(t)
	var mu sync.Mutex
	active := map[net.Conn]bool{}
	var workers sync.WaitGroup
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			active[conn] = true
			mu.Unlock()
			workers.Go(func() {
				defer conn.Close()
				defer func() { mu.Lock(); delete(active, conn); mu.Unlock() }()
				io.Copy(conn, conn)
			})
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		<-done
		mu.Lock()
		for c := range active {
			c.Close()
		}
		mu.Unlock()
		workers.Wait()
	})
	return listener.Addr().String()
}

// controlledStats is a controlled SDK peer, not an actual Xray process. It
// verifies channel receive limits and cancellation separately from traffic proof.
type controlledStats struct {
	stats.UnimplementedStatsServiceServer
	counts, lists   atomic.Int32
	mu              sync.Mutex
	peers           map[string]bool
	entered, exited chan struct{}
}

func (s *controlledStats) record(ctx context.Context) {
	p, ok := peer.FromContext(ctx)
	if ok {
		s.mu.Lock()
		s.peers[p.Addr.String()] = true
		s.mu.Unlock()
	}
}

func (s *controlledStats) GetStatsOnline(ctx context.Context, r *stats.GetStatsRequest) (*stats.GetStatsResponse, error) {
	s.counts.Add(1)
	s.record(ctx)
	switch r.Name {
	case "user>>>unsupported>>>online":
		return nil, status.Error(codes.Unimplemented, "private native diagnostic")
	case "user>>>blocked>>>online":
		close(s.entered)
		defer close(s.exited)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return &stats.GetStatsResponse{Stat: &stats.Stat{Name: r.Name, Value: 1}}, nil
}

func (s *controlledStats) GetStatsOnlineIpList(ctx context.Context, r *stats.GetStatsRequest) (*stats.GetStatsOnlineIpListResponse, error) {
	s.lists.Add(1)
	s.record(ctx)
	key := "native-ip"
	if r.Name == "user>>>oversized>>>online" {
		key = strings.Repeat("x", 70<<10)
	}
	return &stats.GetStatsOnlineIpListResponse{Name: r.Name, Ips: map[string]int64{key: -1}}, nil
}

func newControlledRPC(t *testing.T) (*rpc.Client, *grpc.Server, *controlledStats) {
	t.Helper()
	listener := listen(t)
	server := grpc.NewServer()
	peer := &controlledStats{peers: map[string]bool{}, entered: make(chan struct{}), exited: make(chan struct{})}
	stats.RegisterStatsServiceServer(server, peer)
	done := make(chan struct{})
	go func() { defer close(done); server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); <-done })
	client, err := rpc.New(rpc.Config{Network: "tcp", Address: listener.Addr().String()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return client, server, peer
}
