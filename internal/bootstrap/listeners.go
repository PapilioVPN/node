package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"

	"github.com/PapilioVPN/node/internal/engines/hysteria"
	wgevents "github.com/PapilioVPN/node/internal/engines/wireguard/diagnostics/events"
	"github.com/PapilioVPN/node/internal/engines/xray/logs/live"
	"github.com/PapilioVPN/node/internal/settings"
	"go.uber.org/fx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func registerListeners(
	lifecycle fx.Lifecycle,
	shutdown fx.Shutdowner,
	config settings.Management,
	logger *slog.Logger,
	readiness *health.Server,
	httpServer *http.Server,
	grpcServer *grpc.Server,
	xrayLogs *live.Service,
	wireguardLogs *wgevents.Service,
	hysteriaEngine *hysteria.Engine,
) {
	var serving sync.WaitGroup
	serveFailed := func(protocol string, err error) {
		readiness.Shutdown()
		logger.Error("management listener failed", "protocol", protocol, "error", err)
		if err := shutdown.Shutdown(fx.ExitCode(1)); err != nil {
			logger.Error("request shutdown", "error", err)
		}
	}
	lifecycle.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			var listen net.ListenConfig
			httpListener, err := listen.Listen(ctx, "tcp", httpServer.Addr)
			if err != nil {
				return fmt.Errorf("listen for HTTP: %w", err)
			}
			grpcListener, err := listen.Listen(ctx, "tcp", config.GRPCAddress)
			if err != nil {
				return errors.Join(fmt.Errorf("listen for gRPC: %w", err), httpListener.Close())
			}
			if err := ctx.Err(); err != nil {
				return errors.Join(err, httpListener.Close(), grpcListener.Close())
			}

			readiness.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
			serving.Go(func() {
				if err := serveHTTP(httpServer, httpListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
					serveFailed("HTTP", err)
				}
			})
			serving.Go(func() {
				if err := grpcServer.Serve(grpcListener); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
					serveFailed("gRPC", err)
				}
			})
			logger.Info("management listeners ready", "http", httpListener.Addr(), "grpc", grpcListener.Addr())
			return nil
		},
		OnStop: func(ctx context.Context) error {
			readiness.Shutdown()
			xrayLogs.Close()
			_ = wireguardLogs.Close(ctx)
			hysteriaEngine.CloseLogs()
			grpcDone := make(chan struct{})
			go func() {
				defer close(grpcDone)
				grpcServer.GracefulStop()
			}()

			httpErr := httpServer.Shutdown(ctx)
			if httpErr != nil {
				httpErr = errors.Join(httpErr, httpServer.Close())
			}
			var grpcErr error
			select {
			case <-grpcDone:
			case <-ctx.Done():
				grpcErr = ctx.Err()
				grpcServer.Stop()
				<-grpcDone
			}
			serving.Wait()
			return errors.Join(httpErr, grpcErr)
		},
	})
}
