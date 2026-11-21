package bootstrap_test

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/bootstrap"
	"github.com/PapilioVPN/node/internal/settings"
	"go.uber.org/fx"
)

type trackedResource struct{ closed atomic.Bool }

func (r *trackedResource) Close() error { r.closed.Store(true); return nil }

// The native Fx hook deliberately ignores the stop deadline until it expires.
// This is a controlled lifecycle fixture, not a claim of real kernel behavior.
func TestRunClosesFinalResourcesWhenExpiredStopSkipsTheirHook(t *testing.T) {
	resource := &trackedResource{}
	var ownerStop, startRan, stopRan atomic.Bool
	config := settings.Config{StartupTimeout: time.Second, ShutdownTimeout: 20 * time.Millisecond}
	code := bootstrap.Run(config, slog.New(slog.NewTextHandler(io.Discard, nil)),
		fx.Provide(fx.Annotate(func() io.Closer { return resource }, fx.ResultTags(`group:"final-resources"`))),
		fx.Invoke(func(lifecycle fx.Lifecycle, shutdown fx.Shutdowner) {
			lifecycle.Append(fx.Hook{OnStop: func(context.Context) error { ownerStop.Store(true); return nil }})
			lifecycle.Append(fx.Hook{OnStart: func(context.Context) error { startRan.Store(true); return shutdown.Shutdown() }, OnStop: func(ctx context.Context) error { stopRan.Store(true); <-ctx.Done(); return ctx.Err() }})
		}),
	)
	if code != 1 || !startRan.Load() || !stopRan.Load() || !resource.closed.Load() || ownerStop.Load() {
		t.Fatalf("code=%d final_close=%v native_owner_stop=%v", code, resource.closed.Load(), ownerStop.Load())
	}
}
