package integration_test

import (
	"bufio"
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

	bridgegrpc "github.com/PapilioVPN/go-bridge/api/grpc/server"
	bridgehttp "github.com/PapilioVPN/go-bridge/api/http/server"
	"github.com/PapilioVPN/node/internal/engines/xray"
	instancesv1 "github.com/PapilioVPN/node/internal/engines/xray/instances/bridge/grpc/proto/v1"
	logsgrpc "github.com/PapilioVPN/node/internal/engines/xray/logs/bridge/grpc"
	logsv1 "github.com/PapilioVPN/node/internal/engines/xray/logs/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

const (
	logPath = "/v1/xray/instances/primary/logs"
	key     = "test-logs"
)

type logEvent struct {
	Execution uint64 `json:"execution,string"`
	Sequence  uint64 `json:"sequence,string"`
	Received  int64  `json:"received_unix_nano,string"`
	Source    string `json:"source"`
	Message   []byte `json:"message"`
}

type fixture struct {
	t                  *testing.T
	ctx                context.Context
	http               *httptest.Server
	rpcAddress         string
	rpc                *grpc.ClientConn
	instances          instancesv1.XrayInstancesServiceClient
	proxy              string
	httpDone, grpcDone chan struct{}
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	executable := os.Getenv("NODE_TEST_XRAY")
	if executable == "" {
		t.Skip("set NODE_TEST_XRAY to the absolute path of Xray v26.11.9")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	t.Cleanup(cancel)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(origin.Close)
	address := reserveAddress(t)
	_, port, err := net.SplitHostPort(address)
	require(t, err)
	dir, err := os.MkdirTemp("", "node-logs-")
	require(t, err)
	t.Cleanup(func() { require(t, os.RemoveAll(dir)) })
	path := filepath.Join(dir, "xray.json")
	config := fmt.Sprintf(`{
		"log":{"error":"/dev/stderr","loglevel":"info"},
		"inbounds":[{"listen":"127.0.0.1","port":%s,"protocol":"http","tag":"proxy"}],
		"outbounds":[{"protocol":"freedom","tag":"direct","settings":{"redirect":%q}}]
	}`, port, origin.Listener.Addr().String())
	require(t, os.WriteFile(path, []byte(config), 0600))

	httpDone := make(chan struct{}, 32)
	grpcDone := make(chan struct{}, 32)
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler { return bridgehttp.RequireAPIKey(key, next) })
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
			httpDone <- struct{}{}
		})
	})
	server, err := bridgegrpc.NewServer(bridgegrpc.Config{APIKey: key}, grpc.InTapHandle(logsgrpc.TransportContext),
		grpc.ChainStreamInterceptor(func(srv any, stream grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
			defer func() { grpcDone <- struct{}{} }()
			return handler(srv, stream)
		}))
	require(t, err)
	app := fx.New(fx.NopLogger, xray.Module, fx.Supply(router, server, settings.Xray{
		ID: "primary", Executable: executable, ConfigFile: path, RuntimeDirectory: dir,
	}))
	require(t, app.Start(ctx))
	t.Cleanup(func() {
		stop, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		require(t, app.Stop(stop))
	})
	httpServer := httptest.NewUnstartedServer(router)
	httpServer.Listener = smallWriteBuffer{httpServer.Listener}
	httpServer.Start()
	t.Cleanup(httpServer.Close)

	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	require(t, err)
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		if err := <-done; err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			t.Error(err)
		}
	})
	rpc := connectRPC(t, listener.Addr().String())
	f := fixture{t, metadata.AppendToOutgoingContext(ctx, "x-api-key", key), httpServer,
		listener.Addr().String(), rpc, instancesv1.NewXrayInstancesServiceClient(rpc), address, httpDone, grpcDone}
	f.run(true)
	return f
}

func connectRPC(t *testing.T, addr string) *grpc.ClientConn {
	t.Helper()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStaticStreamWindowSize(64<<10), grpc.WithStaticConnWindowSize(64<<10))
	require(t, err)
	t.Cleanup(func() { require(t, conn.Close()) })
	return conn
}

func (f fixture) run(start bool) {
	f.t.Helper()
	call := f.instances.Stop
	if start {
		call = f.instances.Start
	}
	_, err := call(f.ctx, &instancesv1.XrayInstanceRequest{Id: "primary"})
	require(f.t, err)
}

func (f fixture) httpLog(ctx context.Context, client *http.Client) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.http.URL+logPath, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-API-Key", key)
	return client.Do(req)
}

func (f fixture) rpcLog(ctx context.Context, conn *grpc.ClientConn) (logsv1.XrayLogsService_GetLogsClient, error) {
	s, err := logsv1.NewXrayLogsServiceClient(conn).GetLogs(ctx, &logsv1.XrayLogsRequest{Id: "primary"})
	if err == nil {
		_, err = s.Header()
	}
	return s, err
}

func probe(ctx context.Context, addr, marker string) error {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp4", addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return err
	}
	host := marker + ".invalid:80"
	if _, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", host, host); err != nil {
		return err
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Xray CONNECT returned %s", response.Status)
	}
	if _, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: fixture\r\nConnection: close\r\n\r\n"); err != nil {
		return err
	}
	response, err = http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("origin returned %s through Xray", response.Status)
	}
	return nil
}

func receiveHTTP(response *http.Response) func() (logEvent, error) {
	decoder := json.NewDecoder(response.Body)
	return func() (logEvent, error) {
		var event logEvent
		err := decoder.Decode(&event)
		return event, err
	}
}

func receiveGRPC(stream logsv1.XrayLogsService_GetLogsClient) func() (logEvent, error) {
	return func() (logEvent, error) {
		e, err := stream.Recv()
		if err != nil {
			return logEvent{}, err
		}
		source := "stdout"
		if e.GetSource() == logsv1.XrayLogSource_XRAY_LOG_SOURCE_STDERR {
			source = "stderr"
		} else if e.GetSource() != logsv1.XrayLogSource_XRAY_LOG_SOURCE_STDOUT {
			return logEvent{}, fmt.Errorf("invalid source %s", e.GetSource())
		}
		return logEvent{e.GetExecution(), e.GetSequence(), e.GetReceivedUnixNano(), source, e.GetMessage()}, nil
	}
}

func reserveAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	require(t, err)
	defer listener.Close()
	return listener.Addr().String()
}

type smallWriteBuffer struct{ net.Listener }

func (listener smallWriteBuffer) Accept() (net.Conn, error) {
	conn, err := listener.Listener.Accept()
	if err == nil {
		if err := conn.(*net.TCPConn).SetWriteBuffer(4096); err != nil {
			conn.Close()
			return nil, err
		}
	}
	return conn, err
}

func require(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func drain(receive func() (logEvent, error), execution uint64) error {
	for {
		event, err := receive()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if event.Execution != execution {
			return fmt.Errorf("old stream received event from execution %d", event.Execution)
		}
	}
}
