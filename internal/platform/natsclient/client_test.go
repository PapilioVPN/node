//go:build integration

package natsclient_test

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/platform/natsclient"
	"github.com/PapilioVPN/node/internal/shared/mtls"
	"github.com/PapilioVPN/node/internal/testutil"
	"github.com/nats-io/nats.go"
	"go.uber.org/fx"
)

func TestConnectionOutlivesStartupContextAndSupportsNativeRequests(t *testing.T) {
	pki := testutil.NewPKI(t)
	for _, first := range []bool{true, false} {
		name := "TLS before INFO"
		if !first {
			name = "TLS after INFO"
		}
		t.Run(name, func(t *testing.T) {
			broker := testutil.NewNATSBroker(t, pki.Server, first)
			config := clientConfig(strings.Replace(broker.ClientURL(), "nats://", "tls://", 1), pki.Client)
			config.TLSHandshakeFirst = first
			app, client := composeClient(t, config)
			if _, err := client.Conn(); err == nil {
				t.Fatal("connection available before startup")
			}
			startClient(t, app)
			defer stopClient(t, app)
			conn := connection(t, client)
			if _, err := conn.Subscribe("fixture.echo", func(message *nats.Msg) { _ = message.Respond(message.Data) }); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			response, err := conn.RequestWithContext(ctx, "fixture.echo", []byte("hello"))
			if err != nil || string(response.Data) != "hello" {
				t.Fatalf("native request/reply: %v, %v", response, err)
			}
			state, err := conn.TLSConnectionState()
			if err != nil || len(state.VerifiedChains) == 0 {
				t.Fatal("connection did not verify broker TLS")
			}
		})
	}
}

func TestBrokerRejectsInvalidClientAndServerTrust(t *testing.T) {
	pki, foreign := testutil.NewPKI(t), testutil.NewPKI(t)
	broker := testutil.NewNATSBroker(t, pki.Server, true)
	for _, test := range []struct {
		name                 string
		certificate, key, ca string
	}{
		{"foreign client", foreign.Client.Certificate, foreign.Client.PrivateKey, pki.Client.CA},
		{"wrong client usage", pki.Server.Certificate, pki.Server.PrivateKey, pki.Client.CA},
		{"untrusted broker", pki.Client.Certificate, pki.Client.PrivateKey, foreign.Client.CA},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := clientConfig(strings.Replace(broker.ClientURL(), "nats://", "tls://", 1), pki.Client)
			config.TLS.Certificate, config.TLS.PrivateKey, config.TLS.CA = test.certificate, test.key, test.ca
			app, _ := composeClient(t, config)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := app.Start(ctx); err == nil {
				stopClient(t, app)
				t.Fatal("invalid peer accepted")
			}
		})
	}
	identity, err := mtls.Client(pki.Client)
	if err != nil {
		t.Fatal(err)
	}
	identity.Certificates = nil
	if conn, err := nats.Connect(broker.ClientURL(), nats.Secure(identity), nats.TLSHandshakeFirst(), nats.NoReconnect(), nats.Timeout(time.Second)); err == nil {
		conn.Close()
		t.Fatal("broker accepted a missing client certificate")
	}

}

func TestBrokerACLRejectsForbiddenSubject(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := testutil.NewNATSBroker(t, pki.Server, true)
	app, client := composeClient(t, clientConfig(strings.Replace(broker.ClientURL(), "nats://", "tls://", 1), pki.Client))
	startClient(t, app)
	defer stopClient(t, app)
	conn := connection(t, client)
	denied := make(chan error, 1)
	// The native async handler is replaced only in this test to observe broker ACL
	// rejection; this is neither a Node subject nor Node operation authorization.
	previous := conn.ErrorHandler()
	conn.SetErrorHandler(func(conn *nats.Conn, sub *nats.Subscription, err error) {
		if errors.Is(err, nats.ErrPermissionViolation) {
			select {
			case denied <- err:
			default:
			}
		}
		if previous != nil {
			previous(conn, sub, err)
		}
	})
	if _, err := conn.Subscribe("forbidden.>", func(*nats.Msg) {}); err != nil {
		t.Fatal(err)
	}
	if err := conn.FlushTimeout(time.Second); err != nil {
		t.Fatal(err)
	}
	select {
	case <-denied:
	case <-time.After(time.Second):
		t.Fatal("broker did not enforce subject ACL")
	}
}

func TestStartupCancellationClosesIncompleteHandshake(t *testing.T) {
	pki := testutil.NewPKI(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	t.Cleanup(func() { listener.Close(); <-acceptDone })
	config := clientConfig("tls://"+listener.Addr().String(), pki.Client)
	config.ConnectTimeout = 10 * time.Second
	app, _ := composeClient(t, config)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan error, 1)
	startDone := make(chan struct{})
	go func() { defer close(startDone); started <- app.Start(ctx) }()
	t.Cleanup(func() { cancel(); <-startDone })
	var wire net.Conn
	select {
	case wire = <-accepted:
	case <-time.After(time.Second):
		t.Fatal("client did not dial")
	}
	defer wire.Close()
	cancel()
	select {
	case err := <-started:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("startup cancellation lost: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("incomplete TLS handshake ignored startup cancellation")
	}
	if err := wire.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, wire); err != nil {
		t.Fatalf("canceled handshake socket remains open: %v", err)
	}
	<-acceptDone
}

func TestLaterStartupFailureRollsBackConnection(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := testutil.NewNATSBroker(t, pki.Server, true)
	failure := errors.New("later startup failed")
	app, client := composeClient(t, clientConfig(strings.Replace(broker.ClientURL(), "nats://", "tls://", 1), pki.Client),
		fx.Invoke(func(lifecycle fx.Lifecycle, _ *natsclient.Client) {
			lifecycle.Append(fx.Hook{OnStart: func(context.Context) error { return failure }})
		}))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := app.Start(ctx); !errors.Is(err, failure) {
		t.Fatalf("startup failure lost: %v", err)
	}
	if !connection(t, client).IsClosed() {
		t.Fatal("startup rollback left NATS connection open")
	}
}

func TestExhaustedReconnectsRequestFatalExit(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := testutil.NewNATSBroker(t, pki.Server, true)
	app, client := composeClient(t, clientConfig(strings.Replace(broker.ClientURL(), "nats://", "tls://", 1), pki.Client))
	startClient(t, app)
	broker.Shutdown()
	broker.WaitForShutdown()
	select {
	case signal := <-app.Wait():
		if signal.ExitCode != 1 {
			t.Fatalf("exit code: %d", signal.ExitCode)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("exhausted native reconnects did not stop Node")
	}
	if !connection(t, client).IsClosed() {
		t.Fatal("native connection did not terminate")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := app.Stop(ctx); err == nil {
		t.Fatal("terminal connection failure lost")
	}
}

func TestNativeFailoverReusesConnectionAndRestoresSubscriptions(t *testing.T) {
	pki := testutil.NewPKI(t)
	first := testutil.NewNATSBroker(t, pki.Server, true)
	second := testutil.NewNATSBroker(t, pki.Server, true)
	config := clientConfig(strings.Replace(first.ClientURL(), "nats://", "tls://", 1), pki.Client)
	config.Servers = append(config.Servers, strings.Replace(second.ClientURL(), "nats://", "tls://", 1))
	app, client := composeClient(t, config)
	startClient(t, app)
	defer stopClient(t, app)
	conn := connection(t, client)
	if _, err := conn.Subscribe("fixture.failover", func(msg *nats.Msg) { _ = msg.Respond([]byte("restored")) }); err != nil {
		t.Fatal(err)
	}
	if err := conn.FlushTimeout(time.Second); err != nil {
		t.Fatal(err)
	}
	original := conn.ConnectedAddr()
	connected := conn.StatusChanged(nats.CONNECTED)
	if original == first.Addr().String() {
		first.Shutdown()
		first.WaitForShutdown()
	} else {
		second.Shutdown()
		second.WaitForShutdown()
	}
	select {
	case <-connected:
	case <-time.After(3 * time.Second):
		t.Fatal("native failover did not reconnect")
	}
	if conn.ConnectedAddr() == original {
		t.Fatal("connection did not change brokers")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	msg, err := conn.RequestWithContext(ctx, "fixture.failover", nil)
	if err != nil || string(msg.Data) != "restored" {
		t.Fatalf("subscription recovery: %v, %v", msg, err)
	}
}

func TestReconnectBufferIsBounded(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := testutil.NewNATSBroker(t, pki.Server, true)
	config := clientConfig(strings.Replace(broker.ClientURL(), "nats://", "tls://", 1), pki.Client)
	config.ReconnectWait = time.Second
	app, client := composeClient(t, config)
	startClient(t, app)
	conn := connection(t, client)
	reconnecting := conn.StatusChanged(nats.RECONNECTING)
	broker.Shutdown()
	broker.WaitForShutdown()
	select {
	case <-reconnecting:
	case <-time.After(time.Second):
		t.Fatal("disconnect was not observed")
	}
	// The native limit checks the buffer before appending a message, so one
	// message can cross the threshold. Subsequent work must be rejected.
	if err := conn.Publish("fixture.overflow", make([]byte, config.ReconnectBufferSize+1)); err != nil {
		t.Fatal(err)
	}
	if buffered, err := conn.Buffered(); err != nil || buffered <= config.ReconnectBufferSize {
		t.Fatalf("native threshold behavior changed: %d, %v", buffered, err)
	}
	if err := conn.Publish("fixture.overflow", nil); !errors.Is(err, nats.ErrReconnectBufExceeded) {
		t.Fatalf("reconnect buffer did not reject excess work: %v", err)
	}
	// A disconnected drain must report the inability to flush queued work.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := app.Stop(ctx); !errors.Is(err, nats.ErrConnectionReconnecting) {
		t.Fatalf("disconnected drain cause lost: %v", err)
	}
}
