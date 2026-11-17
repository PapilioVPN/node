//go:build integration

package natsclient_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/shared/mtls"
	"github.com/PapilioVPN/node/internal/testutil"
	"github.com/nats-io/nats.go"
)

func TestDrainCompletesAcceptedRequestBeforeClosing(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := testutil.NewNATSBroker(t, pki.Server, true)
	config := clientConfig(strings.Replace(broker.ClientURL(), "nats://", "tls://", 1), pki.Client)
	app, client := composeClient(t, config)
	startClient(t, app)
	conn := connection(t, client)
	entered, release := make(chan struct{}), make(chan struct{})
	sub, err := conn.Subscribe("fixture.drain", func(msg *nats.Msg) {
		close(entered)
		<-release
		_ = msg.Respond([]byte("complete"))
	})
	if err != nil {
		t.Fatal(err)
	}
	subscriptionClosed := make(chan struct{})
	sub.SetClosedHandler(func(string) { close(subscriptionClosed) })
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
		conn.Close()
		<-subscriptionClosed
	}()
	if err := conn.FlushTimeout(time.Second); err != nil {
		t.Fatal(err)
	}
	identity, err := mtls.Client(pki.Client)
	if err != nil {
		t.Fatal(err)
	}
	requester, err := nats.Connect(config.Servers[0], nats.Secure(identity), nats.TLSHandshakeFirst(), nats.NoReconnect())
	if err != nil {
		t.Fatal(err)
	}
	defer requester.Close()
	response := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		msg, err := requester.RequestWithContext(ctx, "fixture.drain", nil)
		if err == nil && string(msg.Data) != "complete" {
			err = errors.New("drain lost the response")
		}
		response <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("request not accepted")
	}
	statuses := conn.StatusChanged(nats.DRAINING_SUBS)
	stopped := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		stopped <- app.Stop(ctx)
	}()
	select {
	case <-statuses:
	case <-time.After(time.Second):
		t.Fatal("connection did not start draining")
	}
	close(release)
	if err := <-response; err != nil {
		t.Fatal(err)
	}
	if err := <-stopped; err != nil {
		t.Fatalf("drain: %v", err)
	}
	if !conn.IsClosed() {
		t.Fatal("drained connection remains open")
	}
}

func TestDrainTimeoutReportsIncompleteWorkAndClosesConnection(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := testutil.NewNATSBroker(t, pki.Server, true)
	config := clientConfig(strings.Replace(broker.ClientURL(), "nats://", "tls://", 1), pki.Client)
	config.ShutdownTimeout = 100 * time.Millisecond
	app, client := composeClient(t, config)
	startClient(t, app)
	conn := connection(t, client)
	entered, release := make(chan struct{}), make(chan struct{})
	sub, err := conn.Subscribe("fixture.blocked", func(*nats.Msg) { close(entered); <-release })
	if err != nil {
		t.Fatal(err)
	}
	subscriptionClosed := make(chan struct{})
	sub.SetClosedHandler(func(string) { close(subscriptionClosed) })
	defer func() { close(release); conn.Close(); <-subscriptionClosed }()
	if err := conn.Publish("fixture.blocked", nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("message not accepted")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err = app.Stop(ctx)
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, nats.ErrDrainTimeout) {
		t.Fatalf("unfinished drain reported as success: %v", err)
	}
	if !conn.IsClosed() {
		t.Fatal("deadline left NATS connection open")
	}
}
