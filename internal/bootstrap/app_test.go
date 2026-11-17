package bootstrap_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/bootstrap"
	"github.com/PapilioVPN/node/internal/settings"
)

func TestUnconfiguredTransportsAreNotConstructed(t *testing.T) {
	config := settings.Config{StartupTimeout: time.Second, ShutdownTimeout: time.Second}
	app := bootstrap.New(config, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	// No certificate files are present: constructing a transport would fail before startup.
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := app.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}
