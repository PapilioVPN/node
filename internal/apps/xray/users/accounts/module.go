// Package accounts composes the native inbound account capability.
package accounts

import (
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/rpc"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/grpcapi"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/httpapi"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/api"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/configuration"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/natsapi"
	"github.com/PapilioVPN/node/internal/platform/natsclient"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/gofiber/fiber/v3"
	"go.uber.org/fx"
	"google.golang.org/grpc"
)

func Module(requests settings.Requests) fx.Option {
	return fx.Module("xray-accounts", fx.Supply(requests, fx.Private), fx.Provide(compose, composeSources, fx.Private), fx.Invoke(register), fx.Provide(subscribe))
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

func composeSources(service *services.Service, compiler configuration.Compiler) (*services.Sources, error) {
	return services.NewSources(service, configuration.Preparer{Compiler: compiler})
}

type adapters struct {
	fx.In
	Sources  *services.Sources
	Service  *services.Service
	Requests settings.Requests
	HTTP     *fiber.App   `optional:"true"`
	GRPC     *grpc.Server `optional:"true"`
}

func register(in adapters) {
	if in.HTTP != nil {
		httpapi.Handler{Service: in.Service, Sources: in.Sources, Timeout: in.Requests.Timeout}.Register(in.HTTP)
	}
	if in.GRPC != nil {
		grpcapi.Handler{Service: in.Service, Sources: in.Sources, Timeout: in.Requests.Timeout}.Register(in.GRPC)
	}
}
func subscribe(in adapters) natsclient.Subscriptions {
	handler := natsapi.Handler{Service: in.Service, Sources: in.Sources, Timeout: in.Requests.Timeout}
	return natsclient.Subscriptions{Endpoints: []natsclient.Subscription{
		{Name: "Xray account batch addition", SubjectSuffix: "xray.users.accounts.batches.v1.add", Handle: handler.AddMany},
		{Name: "Xray account batch removal", SubjectSuffix: "xray.users.accounts.batches.v1.remove", Handle: handler.RemoveMany},
		{Name: "Xray account source addition", SubjectSuffix: "xray.users.accounts.sources.v1.add", Handle: handler.AddSources},
		{Name: "Xray account addition", SubjectSuffix: "xray.users.accounts.v1.add", Handle: handler.Add},
		{Name: "Xray account removal", SubjectSuffix: "xray.users.accounts.v1.remove", Handle: handler.Remove},
		{Name: "Xray account control status", SubjectSuffix: "xray.users.accounts.status.v1.get", Handle: handler.Status},
		{Name: "Xray account count", SubjectSuffix: "xray.users.accounts.count.v1.get", Handle: handler.Count},
		{Name: "Xray account lookup", SubjectSuffix: "xray.users.accounts.v1.get", Handle: handler.Get},
	}}
}
