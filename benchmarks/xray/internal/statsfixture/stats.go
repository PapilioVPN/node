package statsfixture

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	bridgegrpc "github.com/PapilioVPN/go-bridge/api/grpc/server"
	bridgehttp "github.com/PapilioVPN/go-bridge/api/http/server"
	"github.com/PapilioVPN/node/benchmarks/xray/internal/fixture"
	"github.com/PapilioVPN/node/internal/engines/xray"
	"github.com/PapilioVPN/node/internal/engines/xray/instances/lifecycle"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func Start(t *testing.T, executable string, count int, resetEnabled bool, policy map[string]bool) (*lifecycle.Service, *httptest.Server, *grpc.ClientConn, string) {
	t.Helper()
	proxy, err := net.Listen("tcp4", "127.0.0.1:0")
	Require(t, err)
	proxyAddr := proxy.Addr().String()
	proxyPort := proxy.Addr().(*net.TCPAddr).Port
	Require(t, proxy.Close())

	accounts := make([]map[string]string, count)
	for i := range accounts {
		accounts[i] = map[string]string{"user": Name(i), "pass": "fixture"}
	}
	dir, err := os.MkdirTemp("", "node-users-bench-")
	Require(t, err)
	t.Cleanup(func() { Require(t, os.RemoveAll(dir)) })
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
	Require(t, err)
	Require(t, os.WriteFile(configFile, config, 0600))

	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler { return bridgehttp.RequireAPIKey(fixture.Key, next) })
	httpServer := httptest.NewTLSServer(router)
	t.Cleanup(httpServer.Close)
	server, err := bridgegrpc.NewServer(bridgegrpc.Config{APIKey: fixture.Key, TLS: httpServer.TLS.Clone()})
	Require(t, err)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	Require(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	var owner *lifecycle.Service
	app := fx.New(fx.NopLogger, xray.Module, fx.Supply(router, server, settings.Xray{
		ID: "primary", Executable: executable, ConfigFile: configFile, RuntimeDirectory: dir, ResetEngineStats: resetEnabled,
	}), fx.Populate(&owner))
	start, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	Require(t, app.Start(start))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		Require(t, app.Stop(ctx))
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
	Require(t, err)
	t.Cleanup(func() { Require(t, conn.Close()) })
	return owner, httpServer, conn, proxyAddr
}

func Name(index int) string { return fmt.Sprintf("account-%04d", index) }

func Require(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
