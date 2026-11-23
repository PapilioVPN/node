package integration_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/engines/xray"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestGetLogsRequiresActiveExecution(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "xray.json")
	if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	app := fx.New(fx.NopLogger, xray.Module, fx.Supply(router, grpc.NewServer(), settings.Xray{
		ID: "primary", ConfigFile: path, RuntimeDirectory: dir,
	}))
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := app.Stop(ctx); err != nil {
			t.Error(err)
		}
	}()
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequestWithContext(ctx, http.MethodGet, "/v1/xray/instances/primary/logs", nil))
	res := rec.Result()
	defer res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("GetLogs without an execution = %d %s, want 409", res.StatusCode, body)
	}
}

func TestRealXraySourcesAndExecutionLifetime(t *testing.T) {
	f := newFixture(t)
	recvHTTP, recvRPC := openReaders(t, f, f.ctx)
	require(t, probe(f.ctx, f.proxy, "execution-one"))
	firstHTTP := checkSources(t, recvHTTP, "execution-one")
	firstRPC := checkSources(t, recvRPC, "execution-one")
	if firstHTTP != firstRPC || firstHTTP == 0 {
		t.Fatalf("different executions: HTTP=%d gRPC=%d", firstHTTP, firstRPC)
	}

	oldHTTP, oldRPC := recvHTTP, recvRPC
	f.run(false)
	f.run(true)

	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	recvHTTP, recvRPC = openReaders(t, f, ctx)
	require(t, probe(f.ctx, f.proxy, "execution-two"))
	secondHTTP := checkSources(t, recvHTTP, "execution-two")
	secondRPC := checkSources(t, recvRPC, "execution-two")
	if secondHTTP <= firstHTTP || secondHTTP != secondRPC {
		t.Fatalf("invalid replacement execution: old=%d HTTP=%d gRPC=%d", firstHTTP, secondHTTP, secondRPC)
	}
	require(t, drain(oldHTTP, firstHTTP))
	require(t, drain(oldRPC, firstRPC))
	waitHandler(t, f.httpDone, time.Second)
	waitHandler(t, f.grpcDone, time.Second)

	cancel()
	waitHandler(t, f.httpDone, time.Second)
	waitHandler(t, f.grpcDone, time.Second)
}

func TestRealXraySlowReadersDoNotBlockTrafficOrOtherStreams(t *testing.T) {
	f := newFixture(t)
	slowRPC, err := f.rpcLog(f.ctx, connectRPC(t, f.rpcAddress))
	require(t, err)
	slowClient := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			var dialer net.Dialer
			conn, err := dialer.DialContext(ctx, network, address)
			if err != nil {
				return nil, err
			}
			if err := conn.(*net.TCPConn).SetReadBuffer(4096); err != nil {
				conn.Close()
				return nil, err
			}
			return conn, nil
		},
	}}
	defer slowClient.CloseIdleConnections()
	slowHTTP, err := f.httpLog(f.ctx, slowClient)
	require(t, err)
	defer slowHTTP.Body.Close()

	ctx, cancel := context.WithCancel(f.ctx)
	var readers sync.WaitGroup
	defer func() {
		cancel()
		readers.Wait()
	}()
	recvHTTP, recvRPC := openReaders(t, f, ctx)
	fast := make(chan error, 2)
	for _, receive := range []func() (logEvent, error){recvHTTP, recvRPC} {
		readers.Go(func() {
			for {
				event, err := receive()
				if err != nil || bytes.Contains(event.Message, []byte("end-of-flood.invalid")) {
					fast <- err
					return
				}
			}
		})
	}
	// Never read either slow stream while Xray serves traffic and two peers
	// consume the same output. This exceeds the configured TCP and HTTP/2 windows.
	start := time.Now()
	for i := range 1000 {
		require(t, probe(ctx, f.proxy, fmt.Sprintf("flood-%04d", i)))
	}
	require(t, probe(ctx, f.proxy, "end-of-flood"))
	for range 2 {
		select {
		case err := <-fast:
			require(t, err)
		case <-time.After(3 * time.Second):
			t.Fatal("fast stream did not receive the sentinel")
		}
	}
	if elapsed := time.Since(start); elapsed > 6*time.Second {
		t.Fatalf("Xray traffic and fast delivery took %s", elapsed)
	}
	select {
	case <-f.grpcDone:
		t.Fatal("slow gRPC handler finished before execution stop")
	default:
	}
	cancel()
	waitHandler(t, f.httpDone, time.Second)
	waitHandler(t, f.grpcDone, time.Second)
	f.run(false)
	waitHandler(t, f.grpcDone, time.Second)
	waitHandler(t, f.httpDone, 3*time.Second)

	receiveSlow := receiveHTTP(slowHTTP)
	for {
		event, err := receiveSlow()
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatal(err)
			}
			break
		}
		if bytes.Contains(event.Message, []byte("end-of-flood.invalid")) {
			t.Fatal("the unread HTTP transport did not hit backpressure")
		}
	}

	// Reading after stop can release bytes already buffered by the transports.
	// The unread gRPC stream had ceased forwarding before the final marker.
	for {
		event, err := slowRPC.Recv()
		if err != nil {
			if !errors.Is(err, io.EOF) && status.Code(err) != codes.Canceled {
				t.Fatal(err)
			}
			break
		}
		if bytes.Contains(event.GetMessage(), []byte("end-of-flood.invalid")) {
			t.Fatal("the unread gRPC transport did not hit backpressure")
		}
	}
}

func openReaders(t *testing.T, f fixture, ctx context.Context) (func() (logEvent, error), func() (logEvent, error)) {
	t.Helper()
	response, err := f.httpLog(ctx, f.http.Client())
	require(t, err)
	t.Cleanup(func() { response.Body.Close() })
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "application/x-ndjson" ||
		response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("invalid HTTP log response: %s %v", response.Status, response.Header)
	}
	rpc, err := f.rpcLog(ctx, f.rpc)
	require(t, err)
	return receiveHTTP(response), receiveGRPC(rpc)
}

func checkSources(t *testing.T, receive func() (logEvent, error), marker string) uint64 {
	t.Helper()
	var execution, prev uint64
	sources := make(map[string]bool)
	for len(sources) < 2 {
		event, err := receive()
		require(t, err)
		if event.Sequence <= prev || event.Received <= 0 || event.Execution == 0 ||
			(execution != 0 && event.Execution != execution) ||
			bytes.Contains(event.Message, []byte("execution-one.invalid")) && marker != "execution-one" {
			t.Fatalf("invalid log event: %+v", event)
		}
		execution, prev = event.Execution, event.Sequence
		if bytes.Contains(event.Message, []byte(marker+".invalid")) {
			sources[event.Source] = true
		}
	}
	if !sources["stdout"] || !sources["stderr"] {
		t.Fatalf("unexpected sources: %v", sources)
	}
	return execution
}

func waitHandler(t *testing.T, done <-chan struct{}, timeout time.Duration) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatal("log handler remained active")
	}
}
