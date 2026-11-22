// Package inbounds composes native inbound resources and management adapters.
package inbounds

import (
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/grpcapi"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/httpapi"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/infra/api"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/infra/configuration"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/natsapi"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/rpc"
	"github.com/PapilioVPN/node/internal/platform/natsclient"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/gofiber/fiber/v3"
	"go.uber.org/fx"
	"google.golang.org/grpc"
)

func Module(requests settings.Requests) fx.Option {
	return fx.Module("xray-inbounds", fx.Supply(requests, fx.Private), fx.Provide(compose, fx.Private), fx.Provide(composeCatalog), fx.Invoke(register), fx.Provide(subscribe))
}

type inputs struct {
	fx.In
	Bindings []rpc.Binding `group:"xray-native-api"`
	Status   services.StatusReader
	Compiler configuration.Compiler
}

func compose(in inputs) (*services.Service, *services.Sources, error) {
	factories := make(map[string]configuration.Factory, len(in.Bindings))
	bindings := make([]services.Binding, 0, len(in.Bindings))
	for _, resource := range in.Bindings {
		binding := services.Binding{ID: resource.ID}
		if resource.Client != nil {
			client := api.New(resource.Client)
			binding.Client = client
			binding.Projection = client
			factories[resource.ID] = client
		}
		bindings = append(bindings, binding)
	}
	service, err := services.New(bindings, in.Status)
	if err != nil {
		return nil, nil, err
	}
	sources, err := services.NewSources(service, configuration.Preparer{Compiler: in.Compiler, Factories: factories})
	return service, sources, err
}

func composeCatalog(service *services.Service) *services.Catalog {
	return services.NewCatalog(service, configuration.Inspector{})
}

type adapters struct {
	fx.In
	Catalog  *services.Catalog
	Sources  *services.Sources
	Service  *services.Service
	Requests settings.Requests
	HTTP     *fiber.App   `optional:"true"`
	GRPC     *grpc.Server `optional:"true"`
}

func register(in adapters) {
	if in.HTTP != nil {
		httpapi.Handler{Catalog: in.Catalog, Service: in.Service, Sources: in.Sources, Timeout: in.Requests.Timeout}.Register(in.HTTP)
	}
	if in.GRPC != nil {
		grpcapi.Handler{Catalog: in.Catalog, Service: in.Service, Sources: in.Sources, Timeout: in.Requests.Timeout}.Register(in.GRPC)
	}
}
func subscribe(in adapters) natsclient.Subscriptions {
	handler := natsapi.Handler{Catalog: in.Catalog, Service: in.Service, Sources: in.Sources, Timeout: in.Requests.Timeout}
	return natsclient.Subscriptions{Endpoints: []natsclient.Subscription{
		{Name: "Xray inbound summaries", SubjectSuffix: "xray.proxies.inbounds.summaries.v1.list", Handle: handler.Summaries},
		{Name: "Xray inbound compatibility", SubjectSuffix: "xray.proxies.inbounds.compatibility.v1.get", Handle: handler.Compatibility},
		{Name: "Xray inbound replacement plan", SubjectSuffix: "xray.proxies.inbounds.replacements.v1.plan", Handle: handler.Plan},
		{Name: "Xray inbound replacement", SubjectSuffix: "xray.proxies.inbounds.replacements.v1.apply", Handle: handler.Replace},
		{Name: "Xray inbound configurations", SubjectSuffix: "xray.proxies.inbounds.v1.list", Handle: handler.Records},
		{Name: "Xray inbound tags", SubjectSuffix: "xray.proxies.inbounds.tags.v1.list", Handle: handler.Tags},
		{Name: "Xray inbound source addition", SubjectSuffix: "xray.proxies.inbounds.sources.v1.add", Handle: handler.AddSources},
		{Name: "Xray inbound addition", SubjectSuffix: "xray.proxies.inbounds.v1.add", Handle: handler.Add},
		{Name: "Xray inbound removal", SubjectSuffix: "xray.proxies.inbounds.v1.remove", Handle: handler.Remove},
		{Name: "Xray inbound control status", SubjectSuffix: "xray.proxies.inbounds.status.v1.get", Handle: handler.Status},
	}}
}
