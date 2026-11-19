// Package sensors composes physical sensor observations and management adapters.
package sensors

import (
	"github.com/PapilioVPN/node/internal/apps/system/host/sensors/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/host/sensors/grpcapi"
	"github.com/PapilioVPN/node/internal/apps/system/host/sensors/httpapi"
	"github.com/PapilioVPN/node/internal/apps/system/host/sensors/infra/sys"
	"github.com/PapilioVPN/node/internal/apps/system/host/sensors/natsapi"
	"github.com/PapilioVPN/node/internal/platform/natsclient"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/gofiber/fiber/v3"
	"go.uber.org/fx"
	"google.golang.org/grpc"
)

func Module(requests settings.Requests) fx.Option {
	return fx.Module("sensors", fx.Supply(requests, fx.Private), fx.Provide(func() services.Service { return services.Service{Reader: sys.Reader{}} }, fx.Private), fx.Invoke(register), fx.Provide(subscribe))
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
		{Name: "host temperatures", SubjectSuffix: "system.host.sensors.temperatures.v1.get", Handle: handler.Temperatures},
		{Name: "host fans", SubjectSuffix: "system.host.sensors.fans.v1.get", Handle: handler.Fans},
		{Name: "host power supplies", SubjectSuffix: "system.host.sensors.power_supplies.v1.get", Handle: handler.PowerSupplies},
	}}
}
