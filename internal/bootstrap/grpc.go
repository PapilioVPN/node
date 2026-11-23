package bootstrap

import (
	"crypto/tls"
	"fmt"

	bridgerpc "github.com/PapilioVPN/go-bridge/api/grpc/server"
	logsgrpc "github.com/PapilioVPN/node/internal/engines/xray/logs/bridge/grpc"
	"github.com/PapilioVPN/node/internal/settings"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func newReadiness() *health.Server {
	readiness := health.NewServer()
	readiness.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
	return readiness
}

func newGRPCServer(config settings.Management, tlsConfig *tls.Config, readiness *health.Server) (*grpc.Server, error) {
	server, err := bridgerpc.NewServer(bridgerpc.Config{APIKey: config.APIKey, TLS: tlsConfig}, grpc.InTapHandle(logsgrpc.TransportContext))
	if err != nil {
		return nil, fmt.Errorf("build gRPC server: %w", err)
	}
	healthpb.RegisterHealthServer(server, readiness)
	return server, nil
}
