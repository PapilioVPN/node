// Package processes composes native process observations and management adapters.
package processes

import (
	"github.com/PapilioVPN/node/internal/apps/system/host/processes/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/host/processes/grpcapi"
	"github.com/PapilioVPN/node/internal/apps/system/host/processes/httpapi"
	"github.com/PapilioVPN/node/internal/apps/system/host/processes/infra/proc"
	"github.com/PapilioVPN/node/internal/apps/system/host/processes/natsapi"
	"github.com/PapilioVPN/node/internal/platform/natsclient"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/gofiber/fiber/v3"
	"go.uber.org/fx"
	"google.golang.org/grpc"
)

func Module(requests settings.Requests) fx.Option {
	return fx.Module("processes", fx.Supply(requests, fx.Private),
		fx.Provide(func() (services.Service, error) {
			reader, err := proc.New()
			if err != nil {
				return services.Service{}, err
			}
			return services.Service{Reader: reader, TicksPerSecond: reader.TicksPerSecond}, nil
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
		{Name: "host process count", SubjectSuffix: "system.host.processes.count.v1.get", Handle: handler.Count},
		{Name: "host process", SubjectSuffix: "system.host.processes.v1.get", Handle: handler.Read},
		{Name: "host processes", SubjectSuffix: "system.host.processes.v1.list", Handle: handler.List},
	}}
}
