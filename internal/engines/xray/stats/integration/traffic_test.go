package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/engines/xray"
	"github.com/PapilioVPN/node/internal/engines/xray/instances/lifecycle"
	statsv1 "github.com/PapilioVPN/node/internal/engines/xray/stats/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
)

func TestTrafficCountersWithRealProxy(t *testing.T) {
	executable := os.Getenv("NODE_TEST_XRAY")
	if executable == "" {
		t.Skip("set NODE_TEST_XRAY to the absolute path of Xray v26.11.9")
	}
	testTrafficCounters(t, executable, false)
}

func TestTrafficCountersWithBundledXray(t *testing.T) {
	executable := os.Getenv("NODE_TEST_BUNDLED_XRAY")
	if executable == "" {
		t.Skip("set NODE_TEST_BUNDLED_XRAY to the absolute path of node-xray")
	}
	testTrafficCounters(t, executable, true)
}

func testTrafficCounters(t *testing.T, executable string, scopeBatch bool) {
	t.Helper()
	dir, err := os.MkdirTemp("", "node-traffic-")
	require(t, err)
	t.Cleanup(func() { require(t, os.RemoveAll(dir)) })
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("local traffic"))
	}))
	defer target.Close()

	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	require(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	proxyAddress := listener.Addr().String()
	require(t, listener.Close())

	config := filepath.Join(dir, "xray.json")
	file, err := os.Create(config)
	require(t, err)
	require(t, json.NewEncoder(file).Encode(map[string]any{
		"log":   map[string]any{"loglevel": "none"},
		"stats": map[string]any{},
		"policy": map[string]any{
			"levels": map[string]any{"0": map[string]bool{"statsUserUplink": true, "statsUserDownlink": true, "statsUserOnline": true}},
			"system": map[string]bool{"statsInboundUplink": true, "statsInboundDownlink": true, "statsOutboundUplink": true, "statsOutboundDownlink": true},
		},
		"inbounds": []any{map[string]any{
			"tag": "shared", "listen": "127.0.0.1", "port": port, "protocol": "http",
			"settings": map[string]any{"accounts": []map[string]string{{"user": "case>>>name", "pass": "fixture"}}},
		}},
		"outbounds": []map[string]string{{"tag": "shared", "protocol": "freedom"}},
	}))
	require(t, file.Close())

	router := chi.NewRouter()
	server := grpc.NewServer()
	var owner *lifecycle.Service
	app := fx.New(fx.NopLogger, xray.Module,
		fx.Supply(router, server, settings.Xray{
			ID: "primary", Executable: executable, ConfigFile: config, RuntimeDirectory: dir,
			ResetEngineStats: true, ResetScopeConcurrency: 2, NativeScopeReset: scopeBatch,
		}),
		fx.Populate(&owner))
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	require(t, app.Start(ctx))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		require(t, app.Stop(ctx))
	})
	_, err = owner.Start(ctx, "primary")
	require(t, err)

	proxy := &url.URL{Scheme: "http", Host: proxyAddress, User: url.UserPassword("case>>>name", "fixture")}
	transport := &http.Transport{Proxy: http.ProxyURL(proxy)}
	defer transport.CloseIdleConnections()
	seed := func(t *testing.T) {
		t.Helper()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.URL, nil)
		require(t, err)
		reply, err := (&http.Client{Transport: transport}).Do(request)
		require(t, err)
		body, err := io.ReadAll(reply.Body)
		reply.Body.Close()
		require(t, err)
		if reply.StatusCode != http.StatusOK || string(body) != "local traffic" {
			t.Fatalf("proxy traffic: %s %q", reply.Status, body)
		}
	}
	seed(t)

	listeners := bufconn.Listen(64 << 10)
	done := make(chan error, 1)
	go func() { done <- server.Serve(listeners) }()
	t.Cleanup(func() {
		server.Stop()
		if err := <-done; err != nil && err != grpc.ErrServerStopped {
			t.Error(err)
		}
	})
	conn, err := grpc.NewClient("passthrough:///traffic-test",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listeners.DialContext(ctx)
		}))
	require(t, err)
	t.Cleanup(func() { require(t, conn.Close()) })
	client := statsv1.NewXrayStatsServiceClient(conn)
	const base = "/v1/xray/instances/primary/stats/traffic/"
	for _, selection := range []struct {
		scope string
		name  string
		wire  statsv1.XrayTrafficScope
	}{
		{"user", "case>>>name", statsv1.XrayTrafficScope_XRAY_TRAFFIC_SCOPE_USER},
		{"inbound", "shared", statsv1.XrayTrafficScope_XRAY_TRAFFIC_SCOPE_INBOUND},
		{"outbound", "shared", statsv1.XrayTrafficScope_XRAY_TRAFFIC_SCOPE_OUTBOUND},
	} {
		scope, name := selection.scope, selection.name
		exact, err := client.GetTraffic(ctx, &statsv1.XrayTrafficRequest{Id: "primary", Scope: selection.wire, Name: name})
		require(t, err)
		list, err := client.ListTraffic(ctx, &statsv1.XrayTrafficListRequest{Id: "primary", Scope: selection.wire})
		require(t, err)
		if exact.Name != name || exact.GetUplinkBytes() <= 0 || exact.GetDownlinkBytes() <= 0 ||
			len(list.Entries) != 1 || !proto.Equal(list.Entries[0], exact) {
			t.Fatalf("%s: gRPC exact %v list %v", scope, exact, list)
		}
		for _, query := range []string{"", "?name=" + url.QueryEscape(name)} {
			res := httptest.NewRecorder()
			router.ServeHTTP(res, httptest.NewRequestWithContext(ctx, http.MethodGet, base+scope+query, nil))
			if res.Code != http.StatusOK {
				t.Fatalf("%s%s: HTTP %d %s", scope, query, res.Code, res.Body)
			}
			type usage struct {
				Name     string `json:"name"`
				Uplink   *int64 `json:"uplink_bytes"`
				Downlink *int64 `json:"downlink_bytes"`
			}
			var rows []usage
			if query == "" {
				var list struct {
					Entries []usage `json:"entries"`
				}
				require(t, json.Unmarshal(res.Body.Bytes(), &list))
				rows = list.Entries
			} else {
				var row usage
				require(t, json.Unmarshal(res.Body.Bytes(), &row))
				rows = []usage{row}
			}
			if len(rows) != 1 || rows[0].Name != name || rows[0].Uplink == nil || *rows[0].Uplink <= 0 ||
				rows[0].Downlink == nil || *rows[0].Downlink <= 0 {
				t.Fatalf("%s%s: counters %s", scope, query, res.Body)
			}
		}
	}
	res := httptest.NewRecorder()
	router.ServeHTTP(res, httptest.NewRequestWithContext(ctx, http.MethodGet, base+"user?name=missing", nil))
	if res.Code != http.StatusNotFound {
		t.Fatalf("missing counter: HTTP %d", res.Code)
	}
	user := statsv1.XrayTrafficScope_XRAY_TRAFFIC_SCOPE_USER
	t.Run("online IP statistics", func(t *testing.T) {
		verifyOnlineIPs(t, ctx, router, client, proxyAddress, strings.TrimPrefix(target.URL, "http://"))
	})
	_, err = client.GetTraffic(ctx, &statsv1.XrayTrafficRequest{Id: "primary", Scope: user, Name: "missing"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("missing counter: gRPC %v", err)
	}
	t.Run("traffic counter reset", func(t *testing.T) {
		verifyTrafficReset(t, ctx, router, client)
	})
	t.Run("traffic pair reset", func(t *testing.T) {
		verifyTrafficPairReset(t, ctx, router, client, seed)
	})
	t.Run("traffic scope reset", func(t *testing.T) {
		verifyTrafficScopeReset(t, ctx, router, client, seed)
	})
	_, err = owner.Stop(ctx, "primary")
	require(t, err)
	_, err = client.ListTraffic(ctx, &statsv1.XrayTrafficListRequest{Id: "primary", Scope: user})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("stopped instance: gRPC %v", err)
	}
	_, err = owner.Start(ctx, "primary")
	require(t, err)
	empty, err := client.ListTraffic(ctx, &statsv1.XrayTrafficListRequest{Id: "primary", Scope: user})
	require(t, err)
	if len(empty.Entries) != 0 {
		t.Fatalf("new execution: gRPC counters %v", empty)
	}
	_, err = client.GetOnlineIPCount(ctx, &statsv1.XrayOnlineRequest{Id: "primary", Name: "case>>>name"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("new execution retained a previous online observation: %v", err)
	}
}
