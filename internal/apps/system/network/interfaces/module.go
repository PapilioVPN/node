// Package interfaces composes network interface observations and their adapters.
package interfaces

import (
	"github.com/PapilioVPN/node/internal/apps/system/network/interfaces/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/network/interfaces/grpcapi"
	"github.com/PapilioVPN/node/internal/apps/system/network/interfaces/httpapi"
	"github.com/PapilioVPN/node/internal/apps/system/network/interfaces/infra/netlink"
	"github.com/PapilioVPN/node/internal/apps/system/network/interfaces/natsapi"
	"github.com/PapilioVPN/node/internal/platform/natsclient"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/gofiber/fiber/v3"
	"go.uber.org/fx"
	"google.golang.org/grpc"
)

func Module(requests settings.Requests) fx.Option {
	return fx.Module("interfaces", fx.Supply(requests, fx.Private),
		fx.Provide(func() services.Service {
			return services.Service{Reader: netlink.Reader{Timeout: requests.Timeout}}
		}, fx.Private),
		fx.Invoke(register), fx.Provide(subscribe),
	)
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
		{Name: "network interfaces", SubjectSuffix: "system.network.interfaces.v1.list", Handle: handler.List},
		{Name: "network interface counters", SubjectSuffix: "system.network.interfaces.counters.v1.get", Handle: handler.ReadCounters},
	}}
}
