package integration_test

import (
	"context"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/engines/xray"
	configsv1 "github.com/PapilioVPN/node/internal/engines/xray/configs/bridge/grpc/proto/v1"
	instancesv1 "github.com/PapilioVPN/node/internal/engines/xray/instances/bridge/grpc/proto/v1"
	outboundsv1 "github.com/PapilioVPN/node/internal/engines/xray/outbounds/bridge/grpc/proto/v1"
	routingv1 "github.com/PapilioVPN/node/internal/engines/xray/routing/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

const instanceID = "primary"

type fixture struct {
	http    *httptest.Server
	health  outboundsv1.XrayOutboundsServiceClient
	routing routingv1.XrayRoutingServiceClient
}

func startFixture(t *testing.T, source string) fixture {
	t.Helper()
	executable := os.Getenv("NODE_TEST_XRAY")
	if executable == "" {
		t.Skip("set NODE_TEST_XRAY to the absolute path of Xray v26.11.9")
	}
	// Keep the native Unix socket path below the Linux limit.
	dir, err := os.MkdirTemp("", "node-health-")
	require(t, err)
	t.Cleanup(func() { require(t, os.RemoveAll(dir)) })
	path := filepath.Join(dir, "xray.json")
	require(t, os.WriteFile(path, []byte(source), 0600))
	config := settings.Xray{ID: instanceID, Executable: executable, ConfigFile: path, RuntimeDirectory: dir}
	router, server := chi.NewRouter(), grpc.NewServer()
	app := fx.New(fx.NopLogger, xray.Module, fx.Supply(router, server, config))
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	require(t, app.Start(ctx))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		require(t, app.Stop(ctx))
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
	conn, err := grpc.NewClient("passthrough:///xray-outbound-health",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }))
	require(t, err)
	t.Cleanup(func() { require(t, conn.Close()) })

	validation, err := configsv1.NewXrayConfigsServiceClient(conn).ValidateConfig(ctx, &configsv1.ValidateXrayConfigRequest{Id: instanceID})
	require(t, err)
	if !validation.GetValid() {
		t.Fatalf("native config validation = %v", validation)
	}
	started, err := instancesv1.NewXrayInstancesServiceClient(conn).Start(ctx, &instancesv1.XrayInstanceRequest{Id: instanceID})
	require(t, err)
	if started.State != instancesv1.XrayInstanceState_XRAY_INSTANCE_STATE_READY {
		t.Fatalf("Xray start = %v", started)
	}
	return fixture{http: httpServer, health: outboundsv1.NewXrayOutboundsServiceClient(conn), routing: routingv1.NewXrayRoutingServiceClient(conn)}
}

func require(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
