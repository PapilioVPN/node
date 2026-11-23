package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	bridgegrpc "github.com/PapilioVPN/go-bridge/api/grpc/server"
	bridgehttp "github.com/PapilioVPN/go-bridge/api/http/server"
	"github.com/PapilioVPN/node/internal/engines/xray"
	"github.com/PapilioVPN/node/internal/engines/xray/instances/lifecycle"
	statsv1 "github.com/PapilioVPN/node/internal/engines/xray/stats/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/xray/stats/native"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestMeasureOnlineUsers(t *testing.T) {
	executable := os.Getenv("XRAY_BENCH_ONLINE_USERS")
	if executable == "" {
		t.Skip("set XRAY_BENCH_ONLINE_USERS to the Xray v26.11.9 binary")
	}
	const total = 1000
	const rounds = 5
	const window = 350 * time.Millisecond
	owner, httpServer, grpcConn, proxy := startStatsFixture(t, executable, total, false, map[string]bool{"statsUserOnline": true})

	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	_, err := owner.Start(ctx, "primary")
	requireStatsFixture(t, err)
	conn, err := owner.NativeConnection("primary")
	requireStatsFixture(t, err)

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("traffic fixture"))
	}))
	defer target.Close()

	httpClient := httpServer.Client()
	transport := httpClient.Transport.(*http.Transport)
	transport.MaxIdleConnsPerHost = 8
	grpcClient := statsv1.NewXrayStatsServiceClient(grpcConn)
	request := &statsv1.XrayOnlineUsersRequest{Id: "primary"}
	nativeRequest := &emptypb.Empty{}
	reads := []struct {
		name string
		call func(context.Context) ([]string, error)
	}{
		{"native", func(ctx context.Context) ([]string, error) {
			var reply native.OnlineUsersResponse
			err := conn.Invoke(ctx, "/xray.app.stats.command.StatsService/GetAllOnlineUsers", nativeRequest, &reply,
				grpc.StaticMethod(), grpc.MaxCallRecvMsgSize(1<<20))
			return reply.GetUsers(), err
		}},
		{"http", func(ctx context.Context) ([]string, error) {
			body, err := trafficHTTPRead(ctx, httpClient, httpServer.URL+"/v1/xray/instances/primary/stats/online/users", nil)
			if err != nil {
				return nil, err
			}
			var reply statsv1.XrayOnlineUsers
			err = json.Unmarshal(body, &reply)
			return reply.GetNames(), err
		}},
		{"grpc", func(ctx context.Context) ([]string, error) {
			reply, err := grpcClient.ListOnlineUsers(metadata.AppendToOutgoingContext(ctx, "x-api-key", key), request)
			return reply.GetNames(), trafficCallError(err)
		}},
	}

	out := csv.NewWriter(os.Stdout)
	requireStatsFixture(t, out.Write([]string{"transport", "round", "accounts", "clients", "success", "rejected", "goodput_per_sec", "p50_us", "p95_us", "p99_us"}))
	active := 0
	for _, count := range []int{0, 100, total} {
		for ; active < count; active++ {
			tunnel, err := openOnlineTunnel(ctx, proxy, strings.TrimPrefix(target.URL, "http://"), statsFixtureName(active))
			requireStatsFixture(t, err)
			defer tunnel.Close()
		}

		for _, read := range reads {
			names, err := read.call(ctx)
			requireStatsFixture(t, err)
			slices.Sort(names)
			if len(names) != count {
				t.Fatalf("%s reported %d accounts, want %d", read.name, len(names), count)
			}
			for i, got := range names {
				want := statsFixtureName(i)
				if read.name == "native" {
					want = "user>>>" + want + ">>>online"
				}
				if got != want {
					t.Fatalf("%s account %d is %q, want %q", read.name, i, got, want)
				}
			}
		}

		for round := range rounds {
			for _, clients := range []int{1, 8} {
				for step := range reads {
					read := reads[(round+step)%len(reads)]
					result, err := pollRuntimeStats(ctx, func(ctx context.Context) (uint32, error) {
						names, err := read.call(ctx)
						if err == nil && len(names) != count {
							err = fmt.Errorf("received %d accounts, want %d", len(names), count)
						}
						return uint32(len(names)), err
					}, clients, window)
					requireStatsFixture(t, err)
					requireStatsFixture(t, out.Write([]string{
						read.name, strconv.Itoa(round + 1), strconv.Itoa(count), strconv.Itoa(clients),
						strconv.Itoa(len(result.timings)), strconv.Itoa(result.rejected),
						fmt.Sprintf("%.2f", float64(len(result.timings))/result.elapsed.Seconds()),
						percentile(result.timings, 50), percentile(result.timings, 95), percentile(result.timings, 99),
					}))
				}
			}
			out.Flush()
			requireStatsFixture(t, out.Error())
		}
	}
}

func startStatsFixture(t *testing.T, executable string, count int, resetEnabled bool, policy map[string]bool) (*lifecycle.Service, *httptest.Server, *grpc.ClientConn, string) {
	t.Helper()
	proxy, err := net.Listen("tcp4", "127.0.0.1:0")
	requireStatsFixture(t, err)
	proxyAddr := proxy.Addr().String()
	proxyPort := proxy.Addr().(*net.TCPAddr).Port
	requireStatsFixture(t, proxy.Close())

	accounts := make([]map[string]string, count)
	for i := range accounts {
		accounts[i] = map[string]string{"user": statsFixtureName(i), "pass": "fixture"}
	}
	dir, err := os.MkdirTemp("", "node-users-bench-")
	requireStatsFixture(t, err)
	t.Cleanup(func() { requireStatsFixture(t, os.RemoveAll(dir)) })
	configFile := filepath.Join(dir, "xray.json")
	config, err := json.Marshal(map[string]any{
		"log":   map[string]string{"loglevel": "none"},
		"stats": map[string]any{},
		"policy": map[string]any{
			"levels": map[string]any{"0": policy},
		},
		"inbounds": []any{map[string]any{
			"protocol": "http", "tag": "fixture", "listen": "127.0.0.1", "port": proxyPort,
			"settings": map[string]any{"accounts": accounts},
		}},
		"outbounds": []map[string]string{{"tag": "direct", "protocol": "freedom"}},
	})
	requireStatsFixture(t, err)
	requireStatsFixture(t, os.WriteFile(configFile, config, 0600))

	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler { return bridgehttp.RequireAPIKey(key, next) })
	httpServer := httptest.NewTLSServer(router)
	t.Cleanup(httpServer.Close)
	server, err := bridgegrpc.NewServer(bridgegrpc.Config{APIKey: key, TLS: httpServer.TLS.Clone()})
	requireStatsFixture(t, err)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	requireStatsFixture(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	var owner *lifecycle.Service
	app := fx.New(fx.NopLogger, xray.Module, fx.Supply(router, server, settings.Xray{
		ID: "primary", Executable: executable, ConfigFile: configFile, RuntimeDirectory: dir, ResetEngineStats: resetEnabled,
	}), fx.Populate(&owner))
	start, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	requireStatsFixture(t, app.Start(start))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		requireStatsFixture(t, app.Stop(ctx))
	})

	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		if err := <-done; err != nil && err != grpc.ErrServerStopped {
			t.Error(err)
		}
	})
	tlsConfig := httpServer.Client().Transport.(*http.Transport).TLSClientConfig
	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
	requireStatsFixture(t, err)
	t.Cleanup(func() { requireStatsFixture(t, conn.Close()) })
	return owner, httpServer, conn, proxyAddr
}

func statsFixtureName(index int) string { return fmt.Sprintf("account-%04d", index) }

func requireStatsFixture(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
