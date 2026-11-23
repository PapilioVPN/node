package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/PapilioVPN/node/internal/engines/xray/logs"
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

func TestStartReportsEarlyExitWhenPortIsOccupied(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	port := listener.Addr().(*net.TCPAddr).Port
	f := newFixture(t, fmt.Sprintf(`{
		"log": {"loglevel": "none"},
		"inbounds": [{"tag":"fixture","listen":"127.0.0.1","port":%d,"protocol":"socks"}]
	}`, port))
	f.expectHTTPStartFailure("exited_before_ready", true)

	_, err = f.client.Start(f.ctx, &instancesv1.XrayInstanceRequest{Id: "primary"})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("Start error = %v, want Unavailable", err)
	}
	details := status.Convert(err).Details()
	if len(details) != 1 {
		t.Fatalf("Start details = %v, want one typed failure", details)
	}
	failure, ok := details[0].(*instancesv1.XrayStartFailure)
	if !ok || failure.GetReason() != instancesv1.XrayStartFailureReason_XRAY_START_FAILURE_REASON_EXITED_BEFORE_READY ||
		failure.ExitCode == nil || failure.GetExitCode() == 0 {
		t.Fatalf("Start details = %v", details)
	}
	f.expectHTTP(http.MethodGet, "", "failed")
	f.expectClean()
}

func TestStartReportsMissingExecutable(t *testing.T) {
	f := newFixtureWithExecutable(t, "{}", filepath.Join(t.TempDir(), "missing"))
	f.expectHTTPStartFailure("launch_failed", false)
	f.expectClean()
}

func TestStartReportsReadinessTimeout(t *testing.T) {
	executable := filepath.Join(t.TempDir(), "fixture")
	// The fixture starts successfully and exits only when Node kills it.
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexec sleep 30\n"), 0700); err != nil {
		t.Fatal(err)
	}
	f := newFixtureWithExecutable(t, "{}", executable)
	f.expectHTTPStartFailure("readiness_timeout", false)
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
	return newFixtureWithExecutable(t, engineConfig, executable)
}

func newFixtureWithExecutable(t *testing.T, engineConfig, executable string) fixture {
	t.Helper()
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
	app := fx.New(fx.NopLogger, instances.Module, logs.Module,
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

func (f fixture) expectHTTPStartFailure(reason string, exitCode bool) {
	f.t.Helper()
	req, err := http.NewRequestWithContext(f.ctx, http.MethodPut, f.http.URL+"/v1/xray/instances/primary/execution", nil)
	if err != nil {
		f.t.Fatal(err)
	}
	response, err := f.http.Client().Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer response.Body.Close()
	var failure struct {
		Reason   string `json:"reason"`
		ExitCode *int32 `json:"exit_code"`
	}
	if response.StatusCode != http.StatusServiceUnavailable || response.Header.Get("Content-Type") != "application/json" {
		f.t.Fatalf("Start = %s, %s", response.Status, response.Header.Get("Content-Type"))
	}
	if err := json.NewDecoder(response.Body).Decode(&failure); err != nil {
		f.t.Fatal(err)
	}
	if failure.Reason != reason || (failure.ExitCode != nil) != exitCode {
		f.t.Fatalf("Start failure = %+v, want reason %s, exit code present %t", failure, reason, exitCode)
	}
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
