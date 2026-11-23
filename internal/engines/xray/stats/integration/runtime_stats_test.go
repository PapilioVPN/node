package integration_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/engines/xray"
	instancesv1 "github.com/PapilioVPN/node/internal/engines/xray/instances/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/xray/instances/lifecycle"
	statsv1 "github.com/PapilioVPN/node/internal/engines/xray/stats/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func TestRuntimeStatsFollowTheActiveExecution(t *testing.T) {
	executable := os.Getenv("NODE_TEST_XRAY")
	if executable == "" {
		t.Skip("set NODE_TEST_XRAY to the absolute path of Xray v26.11.9")
	}
	dir, err := os.MkdirTemp("", "node-stats-")
	require(t, err)
	t.Cleanup(func() { require(t, os.RemoveAll(dir)) })
	configFile := filepath.Join(dir, "xray.json")
	require(t, os.WriteFile(configFile, []byte(`{"log":{"loglevel":"none"}}`), 0600))

	router := chi.NewRouter()
	server := grpc.NewServer()
	var owner *lifecycle.Service
	config := settings.Xray{ID: "primary", Executable: executable, ConfigFile: configFile, RuntimeDirectory: dir}
	app := fx.New(fx.NopLogger, xray.Module, fx.Supply(router, server, config), fx.Populate(&owner))
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	require(t, app.Start(ctx))
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		require(t, app.Stop(stopCtx))
	})
	httpServer := httptest.NewServer(router)
	t.Cleanup(httpServer.Close)

	listener := bufconn.Listen(64 << 10)
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		if err := <-done; err != nil && err != grpc.ErrServerStopped {
			t.Error(err)
		}
	})
	conn, err := grpc.NewClient("passthrough:///xray-stats",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }))
	require(t, err)
	t.Cleanup(func() { require(t, conn.Close()) })
	instances := instancesv1.NewXrayInstancesServiceClient(conn)
	stats := statsv1.NewXrayStatsServiceClient(conn)

	instance := &instancesv1.XrayInstanceRequest{Id: "primary"}
	request := &statsv1.XrayRuntimeStatsRequest{Id: "primary"}
	address := httpServer.URL + "/v1/xray/instances/primary/stats/runtime"
	_, err = stats.GetRuntimeStats(ctx, request)
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("stopped initial state: GetRuntimeStats = %v", err)
	}
	_, err = stats.GetRuntimeStats(ctx, &statsv1.XrayRuntimeStatsRequest{Id: "missing"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("unknown instance: GetRuntimeStats = %v", err)
	}

	_, err = instances.Start(ctx, instance)
	require(t, err)
	original, err := owner.NativeConnection("primary")
	require(t, err)
	rpcResult, err := stats.GetRuntimeStats(ctx, request)
	require(t, err)
	checkStats(t, rpcResult)
	res, err := httpServer.Client().Get(address)
	require(t, err)
	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		t.Fatalf("running instance: HTTP = %s", res.Status)
	}
	var httpResult statsv1.XrayRuntimeStats
	err = json.NewDecoder(res.Body).Decode(&httpResult)
	res.Body.Close()
	require(t, err)
	checkStats(t, &httpResult)
	same, err := owner.NativeConnection("primary")
	require(t, err)
	if same != original {
		t.Fatal("runtime statistics opened a new native connection")
	}

	_, err = instances.Stop(ctx, instance)
	require(t, err)
	if original.GetState() != connectivity.Shutdown {
		t.Fatalf("old native connection state = %v, want Shutdown", original.GetState())
	}
	res, err = httpServer.Client().Get(address)
	require(t, err)
	res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("stopped instance: HTTP = %s", res.Status)
	}
	_, err = instances.Start(ctx, instance)
	require(t, err)
	replacement, err := owner.NativeConnection("primary")
	require(t, err)
	if replacement == original {
		t.Fatal("new execution reused a closed connection")
	}
	rpcResult, err = stats.GetRuntimeStats(ctx, request)
	require(t, err)
	checkStats(t, rpcResult)
}

func checkStats(t *testing.T, value *statsv1.XrayRuntimeStats) {
	t.Helper()
	if value.GetGoroutines() == 0 || value.GetHeapAllocatedBytes() == 0 ||
		value.GetTotalAllocatedBytes() < value.GetHeapAllocatedBytes() ||
		value.GetMallocs() < value.GetFrees() || value.GetLiveObjects() != value.GetMallocs()-value.GetFrees() {
		t.Fatalf("invalid runtime stats: %v", value)
	}
}

func require(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
