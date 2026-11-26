package bootstrap

import (
	"context"
	"crypto/tls"
	"fmt"

	bridgerpc "github.com/PapilioVPN/go-bridge/api/grpc/server"
	"github.com/PapilioVPN/node/internal/engines"
	hysteriagrpc "github.com/PapilioVPN/node/internal/engines/hysteria/logs/bridge/grpc"
	wggrpc "github.com/PapilioVPN/node/internal/engines/wireguard/diagnostics/bridge/grpc"
	logsgrpc "github.com/PapilioVPN/node/internal/engines/xray/logs/bridge/grpc"
	"github.com/PapilioVPN/node/internal/settings"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/tap"
)

func newReadiness() *health.Server {
	readiness := health.NewServer()
	readiness.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
	return readiness
}

func newGRPCServer(config settings.Management, tlsConfig *tls.Config, readiness *health.Server, activity *engines.ManagementActivity) (*grpc.Server, error) {
	options := []grpc.ServerOption{grpc.InTapHandle(logTransportContext)}
	if activity != nil {
		// go-bridge registers the authentication interceptors before these.
		options = append(options,
			grpc.ChainUnaryInterceptor(func(ctx context.Context, request any, _ *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
				activity.Mark()
				defer activity.Mark()
				return next(ctx, request)
			}),
			grpc.ChainStreamInterceptor(func(service any, stream grpc.ServerStream, _ *grpc.StreamServerInfo, next grpc.StreamHandler) error {
				activity.Mark()
				defer activity.Mark()
				return next(service, stream)
			}),
		)
	}
	server, err := bridgerpc.NewServer(bridgerpc.Config{APIKey: config.APIKey, TLS: tlsConfig}, options...)
	if err != nil {
		return nil, fmt.Errorf("build gRPC server: %w", err)
	}
	healthpb.RegisterHealthServer(server, readiness)
	return server, nil
}

func logTransportContext(ctx context.Context, info *tap.Info) (context.Context, error) {
	ctx, err := logsgrpc.TransportContext(ctx, info)
	if err != nil {
		return nil, err
	}
	ctx, err = wggrpc.TransportContext(ctx, info)
	if err != nil {
		return nil, err
	}
	return hysteriagrpc.TransportContext(ctx, info)
}
