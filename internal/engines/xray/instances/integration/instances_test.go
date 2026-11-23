package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/engines/xray/instances"
	instancesv1 "github.com/PapilioVPN/node/internal/engines/xray/instances/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func TestInstanceLifecycle(t *testing.T) {
	f := newFixture(t, `{"log":{"loglevel":"none"}}`)
	f.expectHTTP(http.MethodGet, "", "stopped")
	f.expectHTTP(http.MethodPut, "/execution", "ready")
	f.expectGRPC(f.client.GetStatus, instancesv1.XrayInstanceState_XRAY_INSTANCE_STATE_READY)
	f.expectGRPC(f.client.Stop, instancesv1.XrayInstanceState_XRAY_INSTANCE_STATE_STOPPED)
	f.expectClean()

	f.expectGRPC(f.client.Start, instancesv1.XrayInstanceState_XRAY_INSTANCE_STATE_READY)
	f.expectHTTP(http.MethodGet, "", "ready")
	f.expectHTTP(http.MethodDelete, "/execution", "stopped")
	f.expectClean()

	f.expectGRPC(f.client.Start, instancesv1.XrayInstanceState_XRAY_INSTANCE_STATE_READY)
	f.stop()
	f.expectClean()
}

func TestInvalidNativeConfiguration(t *testing.T) {
	f := newFixture(t, "{invalid")
	_, err := f.client.Start(f.ctx, &instancesv1.XrayInstanceRequest{Id: "primary"})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("Start error = %v, want Unavailable", err)
	}
	f.expectHTTP(http.MethodGet, "", "failed")
	f.expectClean()
}

type fixture struct {
	t      *testing.T
	ctx    context.Context
	stop   func()
	dir    string
	http   *httptest.Server
	client instancesv1.XrayInstancesServiceClient
}

func newFixture(t *testing.T, engineConfig string) fixture {
	t.Helper()
	executable := os.Getenv("NODE_TEST_XRAY")
	if executable == "" {
		t.Skip("set NODE_TEST_XRAY to the absolute path of Xray v26.11.9")
	}
	dir, err := os.MkdirTemp("", "node-native-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	})
	config := filepath.Join(dir, "xray.json")
	if err := os.WriteFile(config, []byte(engineConfig), 0600); err != nil {
		t.Fatal(err)
	}
	runtimeDir := filepath.Join(dir, "run#xray")
	if err := os.Mkdir(runtimeDir, 0700); err != nil {
		t.Fatal(err)
	}
	router, rpcServer := chi.NewRouter(), grpc.NewServer()
	app := fx.New(fx.NopLogger, instances.Module,
		fx.Supply(router, rpcServer, settings.Xray{
			ID: "primary", Executable: executable, ConfigFile: config, RuntimeDirectory: runtimeDir,
		}))
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	t.Cleanup(cancel)
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	stop := func() {
		t.Helper()
		stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := app.Stop(stopCtx); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(stop)

	httpServer := httptest.NewServer(router)
	t.Cleanup(httpServer.Close)
	listener := bufconn.Listen(64 << 10)
	done := make(chan error, 1)
	go func() { done <- rpcServer.Serve(listener) }()
	t.Cleanup(func() {
		rpcServer.Stop()
		if err := <-done; err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			t.Error(err)
		}
	})
	conn, err := grpc.NewClient("passthrough:///instances",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return fixture{t, ctx, stop, runtimeDir, httpServer, instancesv1.NewXrayInstancesServiceClient(conn)}
}

func (f fixture) expectHTTP(method, suffix, want string) {
	f.t.Helper()
	req, err := http.NewRequestWithContext(f.ctx, method, f.http.URL+"/v1/xray/instances/primary"+suffix, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	res, err := f.http.Client().Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		f.t.Fatalf("%s %s = %s: %s", method, suffix, res.Status, body)
	}
	var result struct{ ID, State string }
	if err := json.NewDecoder(res.Body).Decode(&result); err != nil || result.ID != "primary" || result.State != want {
		f.t.Fatalf("%s %s = (%+v, %v), want primary %s", method, suffix, result, err, want)
	}
}

func (f fixture) expectGRPC(call func(context.Context, *instancesv1.XrayInstanceRequest, ...grpc.CallOption) (*instancesv1.XrayInstanceStatus, error), want instancesv1.XrayInstanceState) {
	f.t.Helper()
	result, err := call(f.ctx, &instancesv1.XrayInstanceRequest{Id: "primary"})
	if err != nil || result.GetId() != "primary" || result.GetState() != want {
		f.t.Fatalf("gRPC = (%v, %v), want primary %s", result, err, want)
	}
}

func (f fixture) expectClean() {
	f.t.Helper()
	entries, err := os.ReadDir(f.dir)
	if err != nil || len(entries) != 0 {
		f.t.Fatalf("runtime resources after joined exit = (%v, %v)", entries, err)
	}
}
