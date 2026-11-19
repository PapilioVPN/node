//go:build integration

package httpserver_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/platform/httpserver"
	"github.com/PapilioVPN/node/internal/shared/mtls"
	"github.com/PapilioVPN/node/internal/testutil"
	"github.com/gofiber/fiber/v3"
	"go.uber.org/fx"
)

func TestHTTPRequiresTrustedMutualTLSAndEnforcesBodyLimit(t *testing.T) {
	pki := testutil.NewPKI(t)
	foreign := testutil.NewPKI(t)
	config := serverConfig(pki.Server)
	app, server, addresses := composeServer(t, config)
	var calls atomic.Int32
	server.All("/fixture", func(ctx fiber.Ctx) error {
		calls.Add(1)
		return ctx.SendString("fixture-response")
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopServer(t, app) })
	address := <-addresses
	for _, test := range []struct {
		name          string
		files         mtls.Files
		serverName    string
		noCertificate bool
		wantSuccess   bool
	}{
		{name: "trusted", files: pki.Client, serverName: "localhost", wantSuccess: true},
		{name: "missing client certificate", files: pki.Client, serverName: "localhost", noCertificate: true},
		{name: "foreign client", files: mtls.Files{Certificate: foreign.Client.Certificate, PrivateKey: foreign.Client.PrivateKey, CA: pki.Client.CA}, serverName: "localhost"},
		{name: "untrusted server", files: foreign.Client, serverName: "localhost"},
		{name: "wrong server name", files: pki.Client, serverName: "other.invalid"},
		{name: "wrong client usage", files: pki.Server, serverName: "localhost"},
	} {
		t.Run(test.name, func(t *testing.T) {
			tlsConfig, err := mtls.Client(test.files)
			if err != nil {
				t.Fatal(err)
			}
			tlsConfig.ServerName = test.serverName
			if test.noCertificate {
				tlsConfig.Certificates = nil
			}
			transport := &http.Transport{TLSClientConfig: tlsConfig}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
			before := calls.Load()
			response, err := client.Get("https://" + address + "/fixture")
			if !test.wantSuccess {
				if response != nil {
					response.Body.Close()
				}
				if err == nil || calls.Load() != before {
					t.Fatal("untrusted peer reached the handler")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil || response.StatusCode != http.StatusOK || string(body) != "fixture-response" {
				t.Fatal("trusted request failed")
			}
			response, err = client.Post("https://"+address+"/fixture", "text/plain", strings.NewReader(strings.Repeat("x", config.BodyLimit+1)))
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != http.StatusRequestEntityTooLarge || calls.Load() != before+1 {
				t.Fatal("oversized body reached the handler")
			}
		})
	}
	stopServer(t, app)
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("listener was not released after joined shutdown: %v", err)
	}
	listener.Close()
}

func TestBindFailureKeepsExistingListener(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	config := serverConfig(testutil.NewPKI(t).Server)
	config.Address = occupied.Addr().String()
	app, _, _ := composeServer(t, config)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := app.Start(ctx); !errors.Is(err, syscall.EADDRINUSE) {
		t.Fatalf("expected address conflict, got %v", err)
	}
	connection, err := net.DialTimeout("tcp", config.Address, time.Second)
	if err != nil {
		t.Fatalf("existing listener was damaged: %v", err)
	}
	connection.Close()
}

func TestLaterStartupFailureRollsBackHTTPListener(t *testing.T) {
	failure := errors.New("later resource failed")
	app, _, addresses := composeServer(t, serverConfig(testutil.NewPKI(t).Server), fx.Invoke(func(lifecycle fx.Lifecycle) {
		lifecycle.Append(fx.Hook{OnStart: func(context.Context) error { return failure }})
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := app.Start(ctx); !errors.Is(err, failure) {
		t.Fatalf("expected rollback cause, got %v", err)
	}
	address := <-addresses
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("rollback retained HTTP listener: %v", err)
	}
	listener.Close()
}

func TestUnexpectedHTTPExitRequestsNodeFailure(t *testing.T) {
	pki := testutil.NewPKI(t)
	app, server, addresses := composeServer(t, serverConfig(pki.Server))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopContext, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer stopCancel()
		// The unexpected-exit error is asserted below; still stop after early failures.
		_ = app.Stop(stopContext)
	})
	identity, err := mtls.Client(pki.Client)
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{TLSClientConfig: identity, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second}
	// Fiber's OnListen hook precedes fasthttp's Serve registration. Complete a
	// real request before asking Shutdown to close an already-serving listener.
	response, err := client.Get("https://" + <-addresses + "/unregistered")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("unexpected fixture response: %d", response.StatusCode)
	}
	signal := app.Wait()
	if err := server.ShutdownWithContext(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case stopped := <-signal:
		if stopped.ExitCode != 1 {
			t.Fatalf("unexpected exit status: %d", stopped.ExitCode)
		}
	case <-ctx.Done():
		t.Fatal("server exit left Node running")
	}
	if err := app.Stop(ctx); err == nil {
		t.Fatal("unexpected listener exit was hidden")
	}
}

func TestShutdownDeadlineIsNotReportedAsSuccessfulDrain(t *testing.T) {
	pki := testutil.NewPKI(t)
	config := serverConfig(pki.Server)
	config.ShutdownTimeout = 30 * time.Millisecond
	app, server, addresses := composeServer(t, config)
	entered, release := make(chan struct{}), make(chan struct{})
	server.Get("/fixture", func(ctx fiber.Ctx) error {
		close(entered)
		<-release
		return ctx.SendStatus(http.StatusOK)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	address := <-addresses
	tlsConfig, err := mtls.Client(pki.Client)
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{TLSClientConfig: tlsConfig}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		response, _ := client.Get("https://" + address + "/fixture")
		if response != nil {
			response.Body.Close()
		}
	}()
	t.Cleanup(func() { close(release); <-requestDone })
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("request did not enter the native handler")
	}
	if err := app.Stop(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected a drain deadline, got %v", err)
	}
}

func TestReadinessFollowsListenerLifecycle(t *testing.T) {
	pki := testutil.NewPKI(t)
	var state *httpserver.State
	app, _, addresses := composeServer(t, serverConfig(pki.Server), fx.Populate(&state))
	if state.Ready() {
		t.Fatal("unstarted listener reported ready")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	<-addresses
	if !state.Ready() {
		t.Fatal("started listener not ready")
	}
	stopServer(t, app)
	if state.Ready() {
		t.Fatal("stopped listener reported ready")
	}
}
