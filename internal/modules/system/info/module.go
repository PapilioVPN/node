package info

import (
	"go.uber.org/fx"
	"google.golang.org/grpc"

	"github.com/PapilioVPN/node/internal/modules/system/info/app/services"
	"github.com/PapilioVPN/node/internal/modules/system/info/domain"
	"github.com/PapilioVPN/node/internal/modules/system/info/grpcapi"
	systemv1 "github.com/PapilioVPN/node/internal/modules/system/info/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/settings"
)

// Module owns node information providers and service registration.
func Module(version string, grpcEnabled bool) fx.Option {
	options := []fx.Option{fx.Provide(func(cfg settings.Node, metadata capabilities) *services.InfoService {
		return services.NewInfoService(domain.NodeID(cfg.ID), version, metadata.Values...)
	})}
	if grpcEnabled {
		options = append(options, fx.Invoke(registerGRPC))
	}
	return fx.Module("info", options...)
}

func registerGRPC(registrar grpc.ServiceRegistrar, info *services.InfoService) {
	systemv1.RegisterNodeServiceServer(registrar, grpcapi.Handler{Info: info})
}

// Native Fx contributions describe installed operations, never caller grants.
type capabilities struct {
	fx.In
	Values []domain.Capability `group:"node_capabilities"`
}
