// Package outboundhealth composes native outbound health observations.
package outboundhealth

import (
	"github.com/PapilioVPN/node/internal/apps/xray/observability/outboundhealth/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/outboundhealth/grpcapi"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/outboundhealth/httpapi"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/outboundhealth/infra/api"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/outboundhealth/natsapi"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/rpc"
	"github.com/PapilioVPN/node/internal/platform/natsclient"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/gofiber/fiber/v3"
	"go.uber.org/fx"
	"google.golang.org/grpc"
)

func Module(requests settings.Requests) fx.Option {
	return fx.Module("xray-outbound-health", fx.Supply(requests, fx.Private), fx.Provide(compose, fx.Private), fx.Invoke(register), fx.Provide(subscribe))
}

type inputs struct {
	fx.In
	Bindings []rpc.Binding `group:"xray-native-api"`
	Status   services.StatusReader
}

func compose(in inputs) (*services.Service, error) {
	bindings := make([]services.Binding, 0, len(in.Bindings))
	for _, resource := range in.Bindings {
		b := services.Binding{ID: resource.ID}
		if resource.Client != nil {
			b.Reader = api.New(resource.Client)
		}
		bindings = append(bindings, b)
	}
	return services.New(bindings, in.Status)
}

type adapters struct {
	fx.In
	Service  *services.Service
	Requests settings.Requests
	HTTP     *fiber.App   `optional:"true"`
	GRPC     *grpc.Server `optional:"true"`
}

func register(in adapters) {
	if in.HTTP != nil {
		(httpapi.Handler{Service: in.Service, Timeout: in.Requests.Timeout}).Register(in.HTTP)
	}
	if in.GRPC != nil {
		(grpcapi.Handler{Service: in.Service, Timeout: in.Requests.Timeout}).Register(in.GRPC)
	}
}
func subscribe(in adapters) natsclient.Subscriptions {
	h := natsapi.Handler{Service: in.Service, Timeout: in.Requests.Timeout}
	return natsclient.Subscriptions{Endpoints: []natsclient.Subscription{
		{Name: "Xray outbound health status", SubjectSuffix: "xray.observability.outboundhealth.v1.get", Handle: h.Get},
		{Name: "Xray outbound health list", SubjectSuffix: "xray.observability.outboundhealth.v1.list", Handle: h.List},
	}}
}
