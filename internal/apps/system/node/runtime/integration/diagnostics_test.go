//go:build integration && linux

package integration_test

import (
	"context"
	"errors"
	"github.com/PapilioVPN/node/internal/apps/system/node/runtime/infra/resources"
	"github.com/PapilioVPN/node/internal/platform/natsclient"
	"github.com/PapilioVPN/node/internal/testutil"
	"github.com/nats-io/nats.go"
	"go.uber.org/fx"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestDiagnosticsProbeRealBrokerAndReportClosedConnection(t *testing.T) {
	pki := testutil.NewPKI(t)
	config := nodeConfig(t, pki)
	var client *natsclient.Client
	app := fx.New(fx.NopLogger, fx.Supply(*config.Communications.NATS, slog.New(slog.NewTextHandler(io.Discard, nil))), natsclient.Module, fx.Populate(&client))
	if err := app.Err(); err != nil {
		t.Fatal(err)
	}
	reader := resources.Reader{NATS: client}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	before, err := reader.Read(ctx)
	if err != nil || before.NATS == nil || before.NATS.State != "not_started" || before.NATS.ProbeSucceeded {
		t.Fatalf("%+v %v", before, err)
	}
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stop, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := app.Stop(stop); err != nil && !errors.Is(err, nats.ErrConnectionClosed) {
			t.Error(err)
		}
	})
	ready, err := reader.Read(ctx)
	if err != nil || ready.NATS == nil || !ready.NATS.ProbeSucceeded || ready.HTTP != nil || ready.GRPC != nil {
		t.Fatalf("%+v %v", ready, err)
	}
	conn, err := client.Conn()
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	closed, err := reader.Read(ctx)
	if err != nil || closed.NATS.ProbeSucceeded || closed.NATS.State != "CLOSED" || closed.NATS.ErrorCode != "not_connected" {
		t.Fatalf("%+v %v", closed, err)
	}
}
