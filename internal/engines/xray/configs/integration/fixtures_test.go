package integration_test

import (
	"context"
	"errors"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/engines/xray"
	configsv1 "github.com/PapilioVPN/node/internal/engines/xray/configs/bridge/grpc/proto/v1"
	instancesv1 "github.com/PapilioVPN/node/internal/engines/xray/instances/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const instanceID = "primary"

type fixture struct {
	config    settings.Xray
	http      *httptest.Server
	configs   configsv1.XrayConfigsServiceClient
	instances instancesv1.XrayInstancesServiceClient
}

func newFixture(t testing.TB, content string) fixture {
	t.Helper()
	executable := os.Getenv("NODE_TEST_XRAY")
	if executable == "" {
		t.Skip("set NODE_TEST_XRAY to the absolute path of Xray v26.11.9")
	}
	dir, err := os.MkdirTemp("", "node-configs-")
	require(t, err)
	t.Cleanup(func() { require(t, os.RemoveAll(dir)) })

	config := settings.Xray{
		ID: instanceID, Executable: executable,
		ConfigFile: filepath.Join(dir, "config.json"), RuntimeDirectory: filepath.Join(dir, "runtime"),
	}
	require(t, os.Mkdir(config.RuntimeDirectory, 0700))
	require(t, os.WriteFile(config.ConfigFile, []byte(content), 0600))

	router, server := chi.NewRouter(), grpc.NewServer()
	app := fx.New(fx.NopLogger, xray.Module, fx.Supply(config, router, server))
	require(t, app.Start(t.Context()))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		require(t, app.Stop(ctx))
	})

	httpServer := httptest.NewServer(router)
	t.Cleanup(httpServer.Close)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	require(t, err)
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		if err := <-done; !errors.Is(err, grpc.ErrServerStopped) {
			require(t, err)
		}
	})
	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require(t, err)
	t.Cleanup(func() { require(t, conn.Close()) })
	return fixture{config, httpServer, configsv1.NewXrayConfigsServiceClient(conn), instancesv1.NewXrayInstancesServiceClient(conn)}
}

func require(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
