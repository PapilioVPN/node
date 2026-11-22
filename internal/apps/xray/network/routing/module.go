// Package routing composes live Xray routing management and transport adapters.
package routing

import (
	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/grpcapi"
	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/httpapi"
	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/infra/api"
	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/infra/configuration"
	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/natsapi"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/rpc"
	"github.com/PapilioVPN/node/internal/platform/natsclient"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/gofiber/fiber/v3"
	"go.uber.org/fx"
	"google.golang.org/grpc"
)

func Module(requests settings.Requests) fx.Option {
	return fx.Module("xray-routing", fx.Supply(requests, fx.Private), fx.Provide(compose, fx.Private), fx.Invoke(register), fx.Provide(subscribe))
}

type inputs struct {
	fx.In
	Bindings []rpc.Binding `group:"xray-native-api"`
	Status   services.StatusReader
	Compiler configuration.Compiler `optional:"true"`
}

func compose(in inputs) (*services.Service, error) {
	bindings := make([]services.Binding, 0, len(in.Bindings))
	for _, b := range in.Bindings {
		entry := services.Binding{ID: b.ID}
		if b.Client != nil {
			entry.Backend = api.New(b.Client)
		}
		bindings = append(bindings, entry)
	}
	return services.New(bindings, in.Status, configuration.Preparer{Compiler: in.Compiler})
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
	h := natsapi.Handler{Service: in.Service, Timeout: in.Requests.Timeout}
	return natsclient.Subscriptions{Endpoints: []natsclient.Subscription{
		{Name: "Xray routing rules", SubjectSuffix: "xray.network.routing.rules.v1.list", Handle: h.List},
		{Name: "Xray routing evaluation", SubjectSuffix: "xray.network.routing.v1.test", Handle: h.Probe},
		{Name: "Xray routing rule addition", SubjectSuffix: "xray.network.routing.rules.v1.add", Handle: h.Add},
		{Name: "Xray routing rule removal", SubjectSuffix: "xray.network.routing.rules.v1.remove", Handle: h.Remove},
		{Name: "Xray balancer observation", SubjectSuffix: "xray.network.routing.balancers.v1.get", Handle: h.Balancer},
		{Name: "Xray balancer override", SubjectSuffix: "xray.network.routing.balancers.v1.override", Handle: h.Override},
		{Name: "Xray routing control status", SubjectSuffix: "xray.network.routing.status.v1.get", Handle: h.Status},
	}}
}
