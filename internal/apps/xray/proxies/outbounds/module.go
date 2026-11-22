package outbounds

import (
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/grpcapi"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/httpapi"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/infra/api"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/infra/configuration"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/natsapi"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/rpc"
	"github.com/PapilioVPN/node/internal/platform/natsclient"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/gofiber/fiber/v3"
	"go.uber.org/fx"
	"google.golang.org/grpc"
)

func Module(requests settings.Requests) fx.Option {
	return fx.Module("xray-outbounds", fx.Supply(requests, fx.Private), fx.Provide(compose, composeSources, composeReplacements, fx.Private), fx.Invoke(register), fx.Provide(subscribe))
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

func composeReplacements(service *services.Service, compiler configuration.Compiler) (*services.Replacements, error) {
	return services.NewReplacements(service, configuration.Inspector{}, configuration.Preparer{Compiler: compiler})
}

type adapters struct {
	fx.In
	Sources      *services.Sources
	Replacements *services.Replacements
	Service      *services.Service
	Requests     settings.Requests
	HTTP         *fiber.App   `optional:"true"`
	GRPC         *grpc.Server `optional:"true"`
}

func register(in adapters) {
	if in.HTTP != nil {
		httpapi.Handler{Service: in.Service, Sources: in.Sources, Replacements: in.Replacements, Timeout: in.Requests.Timeout}.Register(in.HTTP)
	}
	if in.GRPC != nil {
		grpcapi.Handler{Service: in.Service, Sources: in.Sources, Replacements: in.Replacements, Timeout: in.Requests.Timeout}.Register(in.GRPC)
	}
}
func subscribe(in adapters) natsclient.Subscriptions {
	handler := natsapi.Handler{Service: in.Service, Sources: in.Sources, Replacements: in.Replacements, Timeout: in.Requests.Timeout}
	return natsclient.Subscriptions{Endpoints: []natsclient.Subscription{
		{Name: "Xray outbound replacement plan", SubjectSuffix: "xray.proxies.outbounds.replacements.v1.plan", Handle: handler.PlanReplacement},
		{Name: "Xray outbound replacement application", SubjectSuffix: "xray.proxies.outbounds.replacements.v1.apply", Handle: handler.ApplyReplacement},
		{Name: "Xray outbound replacement result", SubjectSuffix: "xray.proxies.outbounds.replacements.v1.get", Handle: handler.ReplacementResult},
		{Name: "Xray outbound addition", SubjectSuffix: "xray.proxies.outbounds.v1.add", Handle: handler.Add},
		{Name: "Xray outbound source addition", SubjectSuffix: "xray.proxies.outbounds.sources.v1.add", Handle: handler.AddSources},
		{Name: "Xray outbound removal", SubjectSuffix: "xray.proxies.outbounds.v1.remove", Handle: handler.Remove},
		{Name: "Xray outbound control status", SubjectSuffix: "xray.proxies.outbounds.status.v1.get", Handle: handler.Status},
		{Name: "Xray outbound configurations", SubjectSuffix: "xray.proxies.outbounds.v1.list", Handle: handler.Records},
	}}
}
