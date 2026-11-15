//go:build integration

package bootstrap_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/bootstrap"
	rpc "github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/platform/httpserver"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/shared/testutil"
	"go.uber.org/fx"
)

func httpConfig(certificates testutil.Certificates) settings.Config {
	cfg := certificates.Config()
	cfg.Communications.GRPC.Enabled = false
	cfg.Communications.HTTP = settings.HTTP{
		Enabled: true, Listen: "127.0.0.1:0", RequestTimeout: time.Second, ReadTimeout: 2 * time.Second,
		WriteTimeout: 2 * time.Second, IdleTimeout: time.Second, MaxBodyBytes: 1024, MaxHeaderBytes: 4096,
		MaxConcurrent: 4, MaxConnections: 8, TLS: cfg.Communications.GRPC.TLS,
	}
	return cfg
}

func TestHTTPOnlyRuntime(t *testing.T) {
	certificates := testutil.NewCertificates(t)
	cfg := httpConfig(certificates)
	var server *httpserver.Server
	app := bootstrap.New(cfg, "http-version", fx.Populate(&server), fx.NopLogger)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Stop(context.Background()); _ = server.Close() })
	transport := &http.Transport{TLSClientConfig: certificates.ClientTLS(t)}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second}
	response, err := client.Get("https://" + server.Addr().String() + "/v1/node")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result struct {
		NodeID       string   `json:"node_id"`
		Version      string   `json:"version"`
		Capabilities []string `json:"capabilities"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || result.NodeID != cfg.Node.ID || result.Version != "http-version" || !slices.Contains(result.Capabilities, "xray.user.online.read") {
		t.Fatalf("incorrect composed HTTP operation: %d %+v", response.StatusCode, result)
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("management response is cacheable")
	}
	for _, name := range []string{"missing-client", "untrusted-client", "untrusted-server"} {
		t.Run(name, func(t *testing.T) {
			tls := certificates.ClientTLS(t)
			switch name {
			case "missing-client":
				tls.Certificates = nil
			case "untrusted-client":
				tls.Certificates = testutil.NewCertificates(t).ClientTLS(t).Certificates
			case "untrusted-server":
				tls.RootCAs = testutil.NewCertificates(t).ClientTLS(t).RootCAs
			}
			tr := &http.Transport{TLSClientConfig: tls}
			defer tr.CloseIdleConnections()
			bad := &http.Client{Transport: tr, Timeout: time.Second}
			if r, err := bad.Get("https://" + server.Addr().String() + "/v1/node"); err == nil {
				r.Body.Close()
				t.Fatal("unverified TLS reached HTTP operation")
			}
		})
	}
	address := server.Addr().String()
	if err := app.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("HTTP listener leaked: %v", err)
	}
	listener.Close()
}

func TestCanceledStartupClosesHTTPAndGRPC(t *testing.T) {
	certificates := testutil.NewCertificates(t)
	cfg := httpConfig(certificates)
	cfg.Communications.GRPC.Enabled = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var addresses []string
	err := bootstrap.Run(ctx, cfg, "test", fx.NopLogger,
		fx.Invoke(func(lifecycle fx.Lifecycle, server *httpserver.Server, grpcServer *rpc.Server) {
			lifecycle.Append(fx.Hook{OnStart: func(context.Context) error {
				addresses = append(addresses, server.Addr().String(), grpcServer.Addr().String())
				cancel()
				return context.Canceled
			}})
		}))
	if !errors.Is(err, context.Canceled) || len(addresses) != 2 {
		t.Fatalf("startup cause lost: %v", err)
	}
	for _, address := range addresses {
		listener, err := net.Listen("tcp", address)
		if err != nil {
			t.Fatalf("canceled startup leaked HTTP listener: %v", err)
		}
		listener.Close()
	}
}
