package integration_test

import (
	"context"
	"fmt"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/engines/xray"
	accountsv1 "github.com/PapilioVPN/node/internal/engines/xray/accounts/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/xray/instances/lifecycle"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

const instanceID = "primary"
const accountsPath = "/v1/xray/instances/primary/inbounds/vless/accounts"

type fixture struct {
	http        *httptest.Server
	inboundPort int
	grpc        accountsv1.XrayAccountsServiceClient
	instance    *lifecycle.Service
}

func startFixture(t *testing.T) fixture {
	t.Helper()
	executable := os.Getenv("NODE_TEST_XRAY")
	if executable == "" {
		t.Skip("set NODE_TEST_XRAY to the absolute path of Xray v26.11.9")
	}
	dir, err := os.MkdirTemp("", "node-accounts-")
	require(t, err)
	t.Cleanup(func() { require(t, os.RemoveAll(dir)) })
	port, otherPort := freePorts(t)
	source := fmt.Sprintf(`{
		"log": {"loglevel":"none"},
		"inbounds": [
			{"tag":"vless","listen":"127.0.0.1","port":%d,"protocol":"vless",
				"settings":{"clients":[],"decryption":"none"}},
			{"tag":"trojan","listen":"127.0.0.1","port":%d,"protocol":"trojan","settings":{"clients":[]}}
		],
		"outbounds": [{"tag":"direct","protocol":"freedom","settings":{"finalRules":[{"action":"allow","ip":["127.0.0.1/32"]}]}}]
	}`, port, otherPort)
	file := filepath.Join(dir, "xray.json")
	require(t, os.WriteFile(file, []byte(source), 0600))

	router, server := chi.NewRouter(), grpc.NewServer()
	config := settings.Xray{ID: instanceID, Executable: executable, ConfigFile: file, RuntimeDirectory: dir}
	var instance *lifecycle.Service
	app := fx.New(fx.NopLogger, xray.Module, fx.Supply(router, server, config), fx.Populate(&instance))
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	require(t, app.Start(ctx))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		require(t, app.Stop(ctx))
	})
	_, err = instance.Start(ctx, instanceID)
	require(t, err)
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
	conn, err := grpc.NewClient("passthrough:///xray-accounts", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}))
	require(t, err)
	t.Cleanup(func() { require(t, conn.Close()) })
	return fixture{http: httpServer, inboundPort: port, grpc: accountsv1.NewXrayAccountsServiceClient(conn), instance: instance}
}

func freePorts(t *testing.T) (int, int) {
	t.Helper()
	socket, err := net.Listen("tcp4", "127.0.0.1:0")
	require(t, err)
	defer socket.Close()
	second, err := net.Listen("tcp4", "127.0.0.1:0")
	require(t, err)
	defer second.Close()
	return socket.Addr().(*net.TCPAddr).Port, second.Addr().(*net.TCPAddr).Port
}

func require(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
