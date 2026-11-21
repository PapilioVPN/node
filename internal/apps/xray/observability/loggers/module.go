// Package loggers composes native logger maintenance and its transport adapters.
package loggers

import (
	"github.com/PapilioVPN/node/internal/apps/xray/observability/loggers/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/loggers/grpcapi"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/loggers/httpapi"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/loggers/infra/api"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/loggers/natsapi"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/rpc"
	"github.com/PapilioVPN/node/internal/platform/natsclient"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/gofiber/fiber/v3"
	"go.uber.org/fx"
	"google.golang.org/grpc"
)

func Module(requests settings.Requests) fx.Option {
	return fx.Module("xray-loggers", fx.Supply(requests, fx.Private), fx.Provide(compose, fx.Private), fx.Invoke(register), fx.Provide(subscribe))
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
			binding.Restarter = api.New(resource.Client)
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
	return natsclient.Subscriptions{Endpoints: []natsclient.Subscription{{Name: "Xray logger reopening", SubjectSuffix: "xray.observability.loggers.v1.reopen", Handle: handler.Reopen}, {Name: "Xray logger control status", SubjectSuffix: "xray.observability.loggers.status.v1.get", Handle: handler.Status}}}
}
