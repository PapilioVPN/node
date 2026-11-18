// Package devices composes native device I/O observations and management adapters.
package devices

import (
	"github.com/PapilioVPN/node/internal/apps/system/storage/devices/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/storage/devices/grpcapi"
	"github.com/PapilioVPN/node/internal/apps/system/storage/devices/httpapi"
	"github.com/PapilioVPN/node/internal/apps/system/storage/devices/infra/proc"
	"github.com/PapilioVPN/node/internal/apps/system/storage/devices/natsapi"
	"github.com/PapilioVPN/node/internal/platform/natsclient"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/gofiber/fiber/v3"
	"go.uber.org/fx"
	"google.golang.org/grpc"
)

func Module(requests settings.Requests) fx.Option {
	return fx.Module("devices", fx.Supply(requests, fx.Private),
		fx.Provide(func() (services.Service, error) {
			reader, err := proc.New()
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
		{Name: "device I/O", SubjectSuffix: "system.storage.devices.v1.list", Handle: handler.List},
	}}
}
