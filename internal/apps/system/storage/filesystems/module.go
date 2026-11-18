// Package filesystems composes native filesystem observations and management adapters.
package filesystems

import (
	"github.com/PapilioVPN/node/internal/apps/system/storage/filesystems/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/storage/filesystems/grpcapi"
	"github.com/PapilioVPN/node/internal/apps/system/storage/filesystems/httpapi"
	"github.com/PapilioVPN/node/internal/apps/system/storage/filesystems/infra/sys"
	"github.com/PapilioVPN/node/internal/apps/system/storage/filesystems/natsapi"
	"github.com/PapilioVPN/node/internal/platform/natsclient"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/gofiber/fiber/v3"
	"go.uber.org/fx"
	"google.golang.org/grpc"
)

func Module(requests settings.Requests) fx.Option {
	return fx.Module("filesystems", fx.Supply(requests, fx.Private),
		fx.Provide(func() (services.Service, error) {
			reader, err := sys.New()
			if err != nil {
				return services.Service{}, err
			}
			return services.Service{Reader: reader}, nil
		}, fx.Private), fx.Invoke(register), fx.Provide(subscribe))
}

type adapters struct {
	fx.In
	Service  services.Service
	Requests settings.Requests
	HTTP     *fiber.App   `optional:"true"`
	GRPC     *grpc.Server `optional:"true"`
}

func register(in adapters) {
	if in.HTTP != nil {
		httpapi.Handler{Service: in.Service, Timeout: in.Requests.Timeout}.Register(in.HTTP)
	}
	if in.GRPC != nil {
		grpcapi.Handler{Service: in.Service, Timeout: in.Requests.Timeout}.Register(in.GRPC)
	}
}
func subscribe(in adapters) natsclient.Subscriptions {
	handler := natsapi.Handler{Service: in.Service, Timeout: in.Requests.Timeout}
	return natsclient.Subscriptions{Endpoints: []natsclient.Subscription{
		{Name: "filesystem mounts", SubjectSuffix: "system.storage.filesystems.v1.list", Handle: handler.List},
		{Name: "filesystem usage", SubjectSuffix: "system.storage.filesystems.usage.v1.get", Handle: handler.Usage},
	}}
}
