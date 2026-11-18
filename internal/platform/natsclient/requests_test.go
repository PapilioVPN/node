//go:build integration

package natsclient_test

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/platform/natsclient"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/testutil"
	"github.com/nats-io/nats.go"
	"go.uber.org/fx"
)

func TestActualSubscriptionCountReservesStopBudgetsBeforeStartup(t *testing.T) {
	pki := testutil.NewPKI(t)
	// Composition constructs native TLS but must reject an insufficient stop
	// budget before attempting a broker connection or listener startup.
	for _, budget := range []time.Duration{2 * time.Second, 2*time.Second + time.Nanosecond} {
		app := fx.New(fx.NopLogger,
			fx.Supply(clientConfig("tls://127.0.0.1:1", pki.Client), slog.New(slog.NewTextHandler(io.Discard, nil))),
			natsclient.Module, natsclient.RequestsModule(settings.NATSRequests{SubjectPrefix: "fixture", PendingMessages: 1, PendingBytes: 1024, ShutdownTimeout: time.Second}, budget),
			fx.Provide(func() natsclient.Subscriptions {
				return natsclient.Subscriptions{Endpoints: []natsclient.Subscription{
					{Name: "first", SubjectSuffix: "first", Handle: func(context.Context, []byte, nats.Header) []byte { return nil }},
					{Name: "second", SubjectSuffix: "second", Handle: func(context.Context, []byte, nats.Header) []byte { return nil }},
				}}
			}))
		if budget == 2*time.Second {
			if err := app.Err(); err == nil || !strings.Contains(err.Error(), "drain budgets") {
				t.Fatalf("actual subscriptions did not reserve their budgets: %v", err)
			}
		} else if err := app.Err(); err != nil {
			t.Fatal(err)
		}
	}
}
