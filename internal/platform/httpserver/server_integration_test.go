//go:build integration

package httpserver_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/platform/httpserver"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/shared/testutil"
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
