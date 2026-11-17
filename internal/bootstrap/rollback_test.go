//go:build integration

package bootstrap_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/bootstrap"
	"github.com/PapilioVPN/node/internal/testutil"
)

func TestNATSStartupFailureRollsBackBothListeners(t *testing.T) {
	pki, foreign := testutil.NewPKI(t), testutil.NewPKI(t)
	broker := testutil.NewNATSBroker(t, pki.Server, true)
	config := foundationConfig(pki, broker.ClientURL())
	config.Communications.NATS.TLS.CA = foreign.Client.CA
	addresses := make(chan string, 2)
	logger := slog.New(listenerLog{slog.NewTextHandler(io.Discard, nil), addresses})
	app := bootstrap.New(config, logger)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := app.Start(ctx); err == nil {
		_ = app.Stop(ctx)
		t.Fatal("untrusted broker accepted")
	}
	for range 2 {
		var address string
		select {
		case address = <-addresses:
		case <-ctx.Done():
			t.Fatal("listener was not started before NATS")
		}
		listener, err := net.Listen("tcp", address)
		if err != nil {
			t.Fatalf("startup rollback retained listener %s: %v", address, err)
		}
		listener.Close()
	}
}
