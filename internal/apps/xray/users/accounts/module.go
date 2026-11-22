// Package accounts composes the native inbound account capability.
package accounts

import (
	inboundservices "github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/rpc"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/grpcapi"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/httpapi"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/api"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/configuration"
	inboundreader "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/inbounds"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/natsapi"
	"github.com/PapilioVPN/node/internal/platform/natsclient"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/gofiber/fiber/v3"
	"go.uber.org/fx"
	"google.golang.org/grpc"
)

func Module(requests settings.Requests) fx.Option {
	return fx.Module("xray-accounts", fx.Supply(requests, fx.Private), fx.Provide(compose), fx.Provide(composeSources, composeConfigurations, fx.Private), fx.Invoke(register), fx.Provide(subscribe))
}

type inputs struct {
	fx.In
	Catalog  *inboundservices.Catalog
	Bindings []rpc.Binding `group:"xray-native-api"`
	Status   services.StatusReader
}

func compose(in inputs) (*services.Service, error) {
	bindings := make([]services.Binding, 0, len(in.Bindings))
	for _, resource := range in.Bindings {
		binding := services.Binding{ID: resource.ID}
		if resource.Client != nil {
			client := api.New(resource.Client)
			binding.Client = client
			binding.Reader = client
		}
		bindings = append(bindings, binding)
	}
	return services.New(bindings, in.Status, inboundreader.Reader{Source: in.Catalog}, api.Inspector{})
}

func composeSources(service *services.Service, compiler configuration.Compiler) (*services.Sources, error) {
	return services.NewSources(service, configuration.Preparer{Compiler: compiler})
}

func composeConfigurations(compiler configuration.Compiler) (*services.Configurations, error) {
	return services.NewConfigurations(compiler, configuration.ReplaceUsers)
}

type adapters struct {
	fx.In
	Configurations *services.Configurations
	Sources        *services.Sources
	Service        *services.Service
	Requests       settings.Requests
	HTTP           *fiber.App   `optional:"true"`
	GRPC           *grpc.Server `optional:"true"`
}

func register(in adapters) {
	if in.HTTP != nil {
		httpapi.Handler{Configurations: in.Configurations, Service: in.Service, Sources: in.Sources, Timeout: in.Requests.Timeout}.Register(in.HTTP)
	}
	if in.GRPC != nil {
		grpcapi.Handler{Configurations: in.Configurations, Service: in.Service, Sources: in.Sources, Timeout: in.Requests.Timeout}.Register(in.GRPC)
	}
}
func subscribe(in adapters) natsclient.Subscriptions {
	handler := natsapi.Handler{Configurations: in.Configurations, Service: in.Service, Sources: in.Sources, Timeout: in.Requests.Timeout}
	return natsclient.Subscriptions{Endpoints: []natsclient.Subscription{
		{Name: "Xray account metadata list", SubjectSuffix: "xray.users.accounts.metadata.v1.list", Handle: handler.ListMetadata},
		{Name: "Xray observed account export", SubjectSuffix: "xray.users.accounts.exports.v1.list", Handle: handler.Export},
		{Name: "Xray account metadata lookup", SubjectSuffix: "xray.users.accounts.metadata.v1.get", Handle: handler.GetMetadata},
		{Name: "Xray account compatibility", SubjectSuffix: "xray.users.accounts.compatibility.v1.get", Handle: handler.Compatibility},
		{Name: "Xray account configuration preparation", SubjectSuffix: "xray.users.accounts.configurations.v1.prepare", Handle: handler.PrepareConfiguration},
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
