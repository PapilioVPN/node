package traffic

import (
	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/grpcapi"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/httpapi"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/infra/api"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/natsapi"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/rpc"
	"github.com/PapilioVPN/node/internal/platform/natsclient"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/gofiber/fiber/v3"
	"go.uber.org/fx"
	"google.golang.org/grpc"
)

func Module(requests settings.Requests) fx.Option {
	return fx.Module("xray-traffic", fx.Supply(requests, fx.Private), fx.Provide(compose, fx.Private), fx.Invoke(register), fx.Provide(subscribe))
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
			adapter := api.New(resource.Client)
			binding.Reader = adapter
			binding.Lister = adapter
			binding.Resetter = adapter
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
		(httpapi.Handler{Service: in.Service, Timeout: in.Requests.Timeout}).Register(in.HTTP)
	}
	if in.GRPC != nil {
		(grpcapi.Handler{Service: in.Service, Timeout: in.Requests.Timeout}).Register(in.GRPC)
	}
}
func subscribe(in adapters) natsclient.Subscriptions {
	handler := natsapi.Handler{Service: in.Service, Timeout: in.Requests.Timeout}
	return natsclient.Subscriptions{Endpoints: []natsclient.Subscription{
		{Name: "Xray exact traffic counter", SubjectSuffix: "xray.observability.traffic.v1.get", Handle: handler.Read},
		{Name: "Xray traffic collection", SubjectSuffix: "xray.observability.traffic.v1.list", Handle: handler.List},
		{Name: "Xray traffic pair", SubjectSuffix: "xray.observability.traffic.v1.pair", Handle: handler.Pair},
		{Name: "Xray traffic counter reset", SubjectSuffix: "xray.observability.traffic.v1.reset_counter", Handle: handler.ResetCounter},
		{Name: "Xray traffic pair reset", SubjectSuffix: "xray.observability.traffic.v1.reset_pair", Handle: handler.ResetPair},
		{Name: "Xray traffic scope reset", SubjectSuffix: "xray.observability.traffic.v1.reset_scope", Handle: handler.ResetScope},
		{Name: "Xray traffic reset status", SubjectSuffix: "xray.observability.traffic.v1.reset_status", Handle: handler.ResetStatus},
	}}
}
