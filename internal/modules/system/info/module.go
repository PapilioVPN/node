package info

import (
	"github.com/gofiber/fiber/v3"
	"go.uber.org/fx"
	"google.golang.org/grpc"

	"github.com/PapilioVPN/node/internal/modules/system/info/app/services"
	"github.com/PapilioVPN/node/internal/modules/system/info/domain"
	"github.com/PapilioVPN/node/internal/modules/system/info/grpcapi"
	systemv1 "github.com/PapilioVPN/node/internal/modules/system/info/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/modules/system/info/httpapi"
	"github.com/PapilioVPN/node/internal/modules/system/info/natsapi"
	"github.com/PapilioVPN/node/internal/platform/natsserver"
	"github.com/PapilioVPN/node/internal/settings"
)

// Module owns node information providers and service registration.
func Module(version string, communications settings.Communications) fx.Option {
	options := []fx.Option{fx.Provide(func(cfg settings.Node, metadata capabilities) *services.InfoService {
		return services.NewInfoService(domain.NodeID(cfg.ID), version, metadata.Values...)
	})}
	if communications.GRPC.Enabled {
		options = append(options, fx.Invoke(registerGRPC))
	}
	if communications.HTTP.Enabled {
		options = append(options, fx.Invoke(registerHTTP))
	}
	if communications.NATS.Enabled {
		options = append(options, fx.Invoke(registerNATS))
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

func registerHTTP(router fiber.Router, info *services.InfoService) {
	httpapi.Handler{Info: info}.Register(router)
}

func registerNATS(server *natsserver.Server, info *services.InfoService, cfg settings.NATS) error {
	handler := natsapi.Handler{Info: info, MaxResponseBytes: cfg.MaxResponseBytes}
	return server.Handle("node.info.get", handler.GetNodeInfo)
}
