package system

import (
	"go.uber.org/fx"
	"google.golang.org/grpc"

	nodev1 "github.com/PapilioVPN/node/internal/entrypoints/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/modules/system/app"
	"github.com/PapilioVPN/node/internal/modules/system/domain"
	"github.com/PapilioVPN/node/internal/modules/system/grpcapi"
	"github.com/PapilioVPN/node/internal/settings"
)

// Module owns the application providers for system capabilities.
func Module(version string, communications settings.Communications) fx.Option {
	options := []fx.Option{fx.Provide(func(cfg settings.Config) *app.InfoService {
		return app.NewInfoService(domain.NodeID(cfg.Node.ID), version)
	})}
	if communications.GRPC.Enabled {
		options = append(options, fx.Invoke(registerGRPC))
	}
	return fx.Module("system", options...)
}

func registerGRPC(registrar grpc.ServiceRegistrar, info *app.InfoService) {
	nodev1.RegisterNodeServiceServer(registrar, grpcapi.Handler{Info: info})
}
