package bootstrap

import (
	"context"
	"errors"
	"io"
	"log/slog"

	"github.com/PapilioVPN/node/internal/settings"
	"go.uber.org/fx"
)

// Run uses native Fx startup/signal/shutdown and joins process-owned resources
// afterwards, including when an expired Fx rollback skips their OnStop hook.
func Run(config settings.Config, logger *slog.Logger, extra ...fx.Option) int {
	var final struct {
		fx.In
		Resources []io.Closer `group:"final-resources"`
	}
	app := New(config, logger, append(extra, fx.Populate(&final))...)
	stopped := app.Wait()
	start, cancel := context.WithTimeout(context.Background(), config.StartupTimeout)
	err := app.Start(start)
	cancel()
	code := 0
	if err == nil {
		code = (<-stopped).ExitCode
	}
	stop, cancel := context.WithTimeout(context.Background(), config.ShutdownTimeout)
	err = errors.Join(err, app.Stop(stop))
	cancel()
	for i := len(final.Resources) - 1; i >= 0; i-- {
		err = errors.Join(err, final.Resources[i].Close())
	}
	if err != nil {
		logger.Error("Node stopped with unfinished or failed lifecycle work", "error", err)
		return 1
	}
	return code
}
