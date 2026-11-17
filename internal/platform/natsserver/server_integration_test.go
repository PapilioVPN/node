//go:build integration

package natsserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/platform/natsserver"
	"github.com/PapilioVPN/node/internal/shared/natsresponse"
	"github.com/PapilioVPN/node/internal/testutil"
	"github.com/nats-io/nats.go"
)

func errorCode(t *testing.T, message *nats.Msg, err error) string {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(message.Data, &result); err != nil {
		t.Fatal(err)
	}
	return result.Error.Code
}

func TestRealBrokerBoundsAndDeadlines(t *testing.T) {
	broker := testutil.NewNATSBroker(t)
	cfg := broker.Config()
	cfg.RequestTimeout = 30 * time.Millisecond
	server, err := natsserver.NewServer(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	var calls atomic.Int32
	if err := server.Handle("echo", func(ctx context.Context, message *nats.Msg) error {
		calls.Add(1)
		return natsresponse.JSON(message, struct {
			Value string `json:"value"`
		}{"owned"}, cfg.MaxResponseBytes)
	}); err != nil {
		t.Fatal(err)
	}
	if err := server.Handle("wait", func(ctx context.Context, message *nats.Msg) error {
		<-ctx.Done()
		return natsresponse.Error(message, "deadline_exceeded", "request deadline exceeded")
	}); err != nil {
		t.Fatal(err)
	}
	if err := server.Handle("broker-large", func(ctx context.Context, message *nats.Msg) error {
		return natsresponse.JSON(message, strings.Repeat("x", 5000), cfg.MaxResponseBytes*2)
	}); err != nil {
		t.Fatal(err)
	}
	if err := server.Handle("large", func(ctx context.Context, message *nats.Msg) error {
		return natsresponse.JSON(message, strings.Repeat("x", 5000), cfg.MaxResponseBytes)
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := server.Start(ctx); err != nil {
		t.Fatal(err)
	}
	client := broker.Client(t, "controller")
	request := func(suffix, data string) (*nats.Msg, error) {
		return client.RequestWithContext(ctx, cfg.SubjectPrefix+"."+suffix, []byte(data))
	}
	// A publish without an inbox must not reach an operation.
	if err := client.Publish(cfg.SubjectPrefix+".echo", []byte("{}")); err != nil {
		t.Fatal(err)
	}
	message, err := request("echo", "{}")
	if err != nil || string(message.Data) != `{"value":"owned"}` || calls.Load() != 1 {
		t.Fatalf("incorrect native operation: %v %v calls=%d", message, err, calls.Load())
	}
	message, err = request("echo", strings.Repeat("x", cfg.MaxRequestBytes+1))
	if code := errorCode(t, message, err); code != "resource_exhausted" {
		t.Fatalf("request limit missing: %s", code)
	}
	for _, value := range []string{"invalid", time.Now().Add(-time.Second).Format(time.RFC3339Nano)} {
		msg := nats.NewMsg(cfg.SubjectPrefix + ".echo")
		msg.Data = []byte("{}")
		msg.Header.Set("Papilio-Deadline", value)
		message, err := client.RequestMsgWithContext(ctx, msg)
		code := errorCode(t, message, err)
		if code != "invalid_argument" && code != "deadline_exceeded" {
			t.Fatalf("invalid deadline reached operation: %s", code)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("rejected requests reached application")
	}
	message, err = request("wait", "{}")
	if code := errorCode(t, message, err); code != "deadline_exceeded" {
		t.Fatalf("native deadline not enforced: %s", code)
	}
	message, err = request("large", "{}")
	if code := errorCode(t, message, err); code != "resource_exhausted" {
		t.Fatalf("partial/oversized response accepted: %s", code)
	}
	message, err = request("broker-large", "{}")
	if code := errorCode(t, message, err); code != "resource_exhausted" {
		t.Fatalf("broker response limit missing: %s", code)
	}
	if err := server.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = request("echo", "{}")
	if !errors.Is(err, nats.ErrNoResponders) {
		t.Fatalf("shutdown retained subscription: %v", err)
	}
}

func TestStopCancelsAndJoinsActiveCallback(t *testing.T) {
	broker := testutil.NewNATSBroker(t)
	cfg := broker.Config()
	server, err := natsserver.NewServer(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	entered, finished := make(chan struct{}), make(chan struct{})
	if err := server.Handle("hold", func(ctx context.Context, message *nats.Msg) error {
		close(entered)
		<-ctx.Done()
		close(finished)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := server.Start(ctx); err != nil {
		t.Fatal(err)
	}
	client := broker.Client(t, "controller")
	request, cancelRequest := context.WithCancel(ctx)
	defer cancelRequest()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = client.RequestWithContext(request, cfg.SubjectPrefix+".hold", []byte("{}"))
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	stop, release := context.WithTimeout(ctx, 20*time.Millisecond)
	defer release()
	if err := server.Stop(stop); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stop deadline lost: %v", err)
	}
	select {
	case <-finished:
	default:
		t.Fatal("native callback outlived Stop")
	}
	cancelRequest()
	<-done
}

func TestUnexpectedBrokerCloseReportsFailure(t *testing.T) {
	broker := testutil.NewNATSBroker(t)
	failed := make(chan struct{}, 1)
	server, err := natsserver.NewServer(broker.Config(), func() {
		select {
		case failed <- struct{}{}:
		default:
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := server.Start(ctx); err != nil {
		t.Fatal(err)
	}
	broker.Close()
	select {
	case <-failed:
	case <-ctx.Done():
		t.Fatal("broker loss did not notify process owner")
	}
	if err := server.Close(); err == nil {
		t.Fatal("native connection failure was lost")
	}
}

func TestCanceledINFOHandshake(t *testing.T) {
	certificates := testutil.NewCertificates(t, "node")
	fixture := &testutil.NATSBroker{Certificates: certificates}
	cfg := fixture.Config()
	// Config uses a named node identity; this fixture only needs client files.
	cfg.TLS.CertificateFile, cfg.TLS.PrivateKeyFile = certificates.ClientCertificate, certificates.ClientKey
	cfg.ConnectTimeout = 3 * time.Second
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	cfg.URL = "tls://" + listener.Addr().String()
	server, err := natsserver.NewServer(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	accepted := make(chan net.Conn, 1)
	go func() {
		connection, err := listener.Accept()
		if err == nil {
			accepted <- connection
		}
		close(accepted)
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- server.Start(ctx) }()
	var connection net.Conn
	select {
	case connection = <-accepted:
	case <-time.After(time.Second):
		t.Fatal("native dial did not reach isolated listener")
	}
	defer connection.Close()
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("startup cancellation cause lost: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled startup waited for the connection timeout")
	}
}

func TestBoundedPendingQueueSurvivesBurst(t *testing.T) {
	broker := testutil.NewNATSBroker(t)
	cfg := broker.Config()
	cfg.MaxPendingMessages = 2
	server, err := natsserver.NewServer(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	if err := server.Handle("held", func(ctx context.Context, message *nats.Msg) error {
		if calls.Add(1) == 1 {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
		return natsresponse.JSON(message, true, cfg.MaxResponseBytes)
	}); err != nil {
		t.Fatal(err)
	}
	if err := server.Handle("barrier", func(ctx context.Context, message *nats.Msg) error {
		return natsresponse.JSON(message, true, cfg.MaxResponseBytes)
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := server.Start(ctx); err != nil {
		t.Fatal(err)
	}
	client := broker.Client(t, "controller")
	first := make(chan error, 1)
	go func() {
		_, err := client.RequestWithContext(ctx, cfg.SubjectPrefix+".held", []byte("{}"))
		first <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	for i := 0; i < 100; i++ {
		if err := client.PublishRequest(cfg.SubjectPrefix+".held", nats.NewInbox(), []byte("{}")); err != nil {
			t.Fatal(err)
		}
	}
	// The native connection parses the preceding burst before dispatching this
	// independent subscription. The held operation remains blocked throughout.
	if _, err := client.RequestWithContext(ctx, cfg.SubjectPrefix+".barrier", []byte("{}")); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := server.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if observed := calls.Load(); observed < 1 || observed > int32(cfg.MaxPendingMessages) {
		t.Fatalf("pending work exceeded native bound: %d", observed)
	}
}

func TestBrokerRequiresVerifiedClientAndServerCertificates(t *testing.T) {
	broker := testutil.NewNATSBroker(t)
	for _, name := range []string{"missing-client", "untrusted-client", "untrusted-server"} {
		t.Run(name, func(t *testing.T) {
			tls := broker.Certificates.ClientTLS(t, "controller")
			switch name {
			case "missing-client":
				tls.Certificates = nil
			case "untrusted-client":
				tls.Certificates = testutil.NewCertificates(t).ClientTLS(t).Certificates
			case "untrusted-server":
				tls.RootCAs = testutil.NewCertificates(t).ClientTLS(t).RootCAs
			}
			connection, err := nats.Connect(broker.URL, nats.Secure(tls), nats.NoReconnect(), nats.Timeout(300*time.Millisecond))
			if err == nil {
				connection.Close()
				t.Fatal("unverified identity established broker connection")
			}
		})
	}
}
