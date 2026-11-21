// Package inbounds composes native inbound resources and management adapters.
package inbounds

import (
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/grpcapi"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/httpapi"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/infra/api"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/natsapi"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/rpc"
	"github.com/PapilioVPN/node/internal/platform/natsclient"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/gofiber/fiber/v3"
	"go.uber.org/fx"
	"google.golang.org/grpc"
)

func Module(requests settings.Requests) fx.Option {
	return fx.Module("xray-inbounds", fx.Supply(requests, fx.Private), fx.Provide(compose, fx.Private), fx.Invoke(register), fx.Provide(subscribe))
}

type inputs struct {
	fx.In
	Bindings []rpc.Binding `group:"xray-native-api"`
	Status   services.StatusReader
}

func compose(in inputs) (*services.Service, error) {
	bindings := make([]services.Binding, 0, len(in.Bindings))
	for _, resource := range in.Bindings {
		binding := services.Binding{ID: resource.ID}
		if resource.Client != nil {
			binding.Client = api.New(resource.Client)
		}
		bindings = append(bindings, binding)
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
		httpapi.Handler{Service: in.Service, Timeout: in.Requests.Timeout}.Register(in.HTTP)
	}
	if in.GRPC != nil {
		grpcapi.Handler{Service: in.Service, Timeout: in.Requests.Timeout}.Register(in.GRPC)
	}
}
func subscribe(in adapters) natsclient.Subscriptions {
	handler := natsapi.Handler{Service: in.Service, Timeout: in.Requests.Timeout}
	return natsclient.Subscriptions{Endpoints: []natsclient.Subscription{{Name: "Xray inbound configurations", SubjectSuffix: "xray.proxies.inbounds.v1.list", Handle: handler.Records}, {Name: "Xray inbound tags", SubjectSuffix: "xray.proxies.inbounds.tags.v1.list", Handle: handler.Tags}, {Name: "Xray inbound addition", SubjectSuffix: "xray.proxies.inbounds.v1.add", Handle: handler.Add}, {Name: "Xray inbound removal", SubjectSuffix: "xray.proxies.inbounds.v1.remove", Handle: handler.Remove}, {Name: "Xray inbound control status", SubjectSuffix: "xray.proxies.inbounds.status.v1.get", Handle: handler.Status}}}
}
