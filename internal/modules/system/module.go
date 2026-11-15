package system

import (
	"go.uber.org/fx"
	"google.golang.org/grpc"

	"github.com/PapilioVPN/node/internal/modules/system/app"
	"github.com/PapilioVPN/node/internal/modules/system/domain"
	"github.com/PapilioVPN/node/internal/modules/system/grpcapi"
	systemv1 "github.com/PapilioVPN/node/internal/modules/system/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/settings"
)

// Module owns the application providers for system capabilities.
func Module(version string, grpcEnabled bool) fx.Option {
	options := []fx.Option{fx.Provide(func(cfg settings.Node, metadata capabilities) *app.InfoService {
		return app.NewInfoService(domain.NodeID(cfg.ID), version, metadata.Values...)
	})}
	if grpcEnabled {
		options = append(options, fx.Invoke(registerGRPC))
	}
	return fx.Module("system", options...)
}

func registerGRPC(registrar grpc.ServiceRegistrar, info *app.InfoService) {
	systemv1.RegisterNodeServiceServer(registrar, grpcapi.Handler{Info: info})
}

// Native Fx contributions describe installed operations, never caller grants.
type capabilities struct {
	fx.In
	Values []domain.Capability `group:"node_capabilities"`
}
