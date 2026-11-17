//go:build integration

package httpserver_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/platform/httpserver"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/testutil"
	"github.com/gofiber/fiber/v3"
)

func TestAdmissionBoundsAndForcedShutdown(t *testing.T) {
	certificates := testutil.NewCertificates(t)
	cfg := settings.HTTP{Listen: "127.0.0.1:0", RequestTimeout: time.Second,
		ReadTimeout: time.Second, WriteTimeout: 2 * time.Second, IdleTimeout: time.Second,
		MaxBodyBytes: 8, MaxHeaderBytes: 4096, MaxConcurrent: 1, MaxConnections: 8, TLS: certificates.Config().Communications.GRPC.TLS}
	server, err := httpserver.NewServer(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	finished := make(chan struct{})
	server.Router().Get("/hold", func(c fiber.Ctx) error {
		close(entered)
		<-c.Context().Done()
		close(finished)
		return nil
	})
	server.Router().Get("/failure", func(fiber.Ctx) error { return errors.New("private-native-diagnostics") })
	server.Router().Get("/panic", func(fiber.Ctx) error { panic("private-native-panic") })
	server.Router().Post("/body", func(c fiber.Ctx) error { return c.SendStatus(200) })
	t.Cleanup(func() { _ = server.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := server.Start(ctx); err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{TLSClientConfig: certificates.ClientTLS(t)}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
	url := "https://" + server.Addr().String()
	for _, path := range []string{"/failure", "/panic"} {
		response, err := client.Get(url + path)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		if readErr != nil || response.StatusCode != 500 || strings.Contains(string(body), "private-native") {
			t.Fatalf("unexpected error disclosure: %d %s %v", response.StatusCode, body, readErr)
		}
	}
	response, err := client.Post(url+"/body", "application/json", strings.NewReader("123456789"))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("body limit missing: %d", response.StatusCode)
	}
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		response, err := client.Get(url + "/hold")
		if err == nil {
			response.Body.Close()
		}
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	response, err = client.Get(url + "/hold")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("admission unbounded: %d", response.StatusCode)
	}
	stop, cancelStop := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancelStop()
	if err := server.Stop(stop); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("forced stop lost deadline: %v", err)
	}
	select {
	case <-finished:
	default:
		t.Fatal("Stop returned before handler joined")
	}
	<-requestDone
	listener, err := net.Listen("tcp", server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	listener.Close()
}

// This verifies admission under a finite burst; it is not a throughput benchmark.
func TestConcurrentBurstIsBoundedAndRecovers(t *testing.T) {
	certificates := testutil.NewCertificates(t)
	cfg := settings.HTTP{Listen: "127.0.0.1:0", RequestTimeout: 3 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 4 * time.Second, IdleTimeout: time.Second,
		MaxBodyBytes: 1024, MaxHeaderBytes: 4096, MaxConcurrent: 4, MaxConnections: 64, TLS: certificates.Config().Communications.GRPC.TLS}
	server, err := httpserver.NewServer(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	release, entered := make(chan struct{}), make(chan struct{}, 4)
	var active, peak atomic.Int32
	server.Router().Get("/bounded", func(c fiber.Ctx) error {
		count := active.Add(1)
		defer active.Add(-1)
		for previous := peak.Load(); count > previous; previous = peak.Load() {
			if peak.CompareAndSwap(previous, count) {
				break
			}
		}
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-c.Context().Done():
		}
		return c.SendStatus(200)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	if err := server.Start(ctx); err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{TLSClientConfig: certificates.ClientTLS(t), MaxConnsPerHost: 64}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	var workers sync.WaitGroup
	results := make(chan int, 50)
	t.Cleanup(func() { cancel(); workers.Wait() })
	request := func() {
		workers.Add(1)
		go func() {
			defer workers.Done()
			req, err := http.NewRequestWithContext(ctx, "GET", "https://"+server.Addr().String()+"/bounded", nil)
			if err != nil {
				results <- 0
				return
			}
			response, err := client.Do(req)
			if err != nil {
				results <- 0
				return
			}
			response.Body.Close()
			results <- response.StatusCode
		}()
	}
	for i := 0; i < 4; i++ {
		request()
	}
	for i := 0; i < 4; i++ {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	for i := 0; i < 46; i++ {
		request()
	}
	for i := 0; i < 46; i++ {
		select {
		case code := <-results:
			if code != 429 {
				t.Fatalf("burst did not reject excess work: %d", code)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	close(release)
	for i := 0; i < 4; i++ {
		select {
		case code := <-results:
			if code != 200 {
				t.Fatalf("admitted work failed: %d", code)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	workers.Wait()
	if peak.Load() != 4 || active.Load() != 0 {
		t.Fatalf("request work exceeded bound or leaked: peak=%d active=%d", peak.Load(), active.Load())
	}
	request()
	if code := <-results; code != 200 {
		t.Fatalf("server did not recover after overload: %d", code)
	}
	workers.Wait()
}

func TestForceStopWaitsForCanceledHandlerCleanup(t *testing.T) {
	certificates := testutil.NewCertificates(t)
	cfg := settings.HTTP{Listen: "127.0.0.1:0", RequestTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: 2 * time.Second, IdleTimeout: time.Second,
		MaxBodyBytes: 1024, MaxHeaderBytes: 4096, MaxConcurrent: 1, MaxConnections: 4, TLS: certificates.Config().Communications.GRPC.TLS}
	server, err := httpserver.NewServer(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	finish := func() { releaseOnce.Do(func() { close(release) }) }
	defer finish()
	server.Router().Get("/cleanup", func(c fiber.Ctx) error {
		close(entered)
		<-c.Context().Done()
		close(canceled)
		<-release // Simulate finite native cleanup after cancellation is observed.
		return nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := server.Start(ctx); err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{TLSClientConfig: certificates.ClientTLS(t)}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second}
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		response, err := client.Get("https://" + server.Addr().String() + "/cleanup")
		if err == nil {
			response.Body.Close()
		}
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	stop, releaseStop := context.WithTimeout(ctx, 20*time.Millisecond)
	defer releaseStop()
	stopped := make(chan error, 1)
	go func() { stopped <- server.Stop(stop) }()
	select {
	case <-canceled:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case err := <-stopped:
		t.Fatalf("Stop returned before canceled handler cleaned up: %v", err)
	case <-time.After(10 * time.Millisecond):
	}
	finish()
	select {
	case err := <-stopped:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("stop cause lost: %v", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	<-requestDone
}
