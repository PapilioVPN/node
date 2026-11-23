package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/engines/xray"
	accountsv1 "github.com/PapilioVPN/node/internal/engines/xray/accounts/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/xray/accounts/membership"
	"github.com/PapilioVPN/node/internal/engines/xray/accounts/native"
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

type configuredAccount struct {
	Email string `json:"email"`
	ID    string `json:"id"`
}

func startFixture(t *testing.T, accounts ...configuredAccount) fixture {
	t.Helper()
	executable := os.Getenv("NODE_TEST_XRAY")
	if executable == "" {
		t.Skip("set NODE_TEST_XRAY to the absolute path of Xray v26.11.9")
	}
	port, otherPort := freePorts(t)
	if accounts == nil {
		accounts = []configuredAccount{}
	}
	clients, err := json.Marshal(accounts)
	require(t, err)
	source := fmt.Sprintf(`{
		"log": {"loglevel":"none"},
		"inbounds": [
			{"tag":"vless","listen":"127.0.0.1","port":%d,"protocol":"vless",
				"settings":{"clients":%s,"decryption":"none"}},
			{"tag":"trojan","listen":"127.0.0.1","port":%d,"protocol":"trojan","settings":{"clients":[]}}
		],
		"outbounds": [{"tag":"direct","protocol":"freedom","settings":{"finalRules":[{"action":"allow","ip":["127.0.0.1/32"]}]}}]
	}`, port, clients, otherPort)
	f := startSourceFixture(t, executable, source)
	f.inboundPort = port
	return f
}

func startSourceFixture(t *testing.T, executable, source string) fixture {
	t.Helper()
	dir, err := os.MkdirTemp("", "node-accounts-")
	require(t, err)
	t.Cleanup(func() { require(t, os.RemoveAll(dir)) })
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
	return fixture{http: httpServer, grpc: accountsv1.NewXrayAccountsServiceClient(conn), instance: instance}
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

type testExecution struct{ conn *grpc.ClientConn }

func (e testExecution) NativeConnection(string) (*grpc.ClientConn, error) { return e.conn, nil }

// Intercept acknowledgements after the real Xray has performed the edit.
func (f fixture) interceptEdits(t *testing.T, after func(int32) error) *membership.Service {
	t.Helper()
	direct, err := f.instance.NativeConnection(instanceID)
	require(t, err)
	var writes atomic.Int32
	conn, err := grpc.NewClient(direct.Target(), grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(func(ctx context.Context, method string, req, reply any, conn *grpc.ClientConn, call grpc.UnaryInvoker, opts ...grpc.CallOption) error {
			err := call(ctx, method, req, reply, conn, opts...)
			if err == nil && method == native.AlterInboundMethod {
				return after(writes.Add(1))
			}
			return err
		}))
	require(t, err)
	t.Cleanup(func() { require(t, conn.Close()) })
	return membership.New(native.New(testExecution{conn}))
}
