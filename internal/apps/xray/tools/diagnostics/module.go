// Package diagnostics composes read-only, credential-free Xray diagnostics.
package diagnostics

import (
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/rpc"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/diagnostics/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/diagnostics/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/diagnostics/grpcapi"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/diagnostics/httpapi"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/diagnostics/infra/api"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/diagnostics/infra/process"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/diagnostics/natsapi"
	"github.com/PapilioVPN/node/internal/platform/natsclient"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/gofiber/fiber/v3"
	"go.uber.org/fx"
	"google.golang.org/grpc"
)

func Module(config []settings.Instance, requests settings.Requests) fx.Option {
	return fx.Module("xray-diagnostics", fx.Supply(config, requests, fx.Private), fx.Provide(compose, fx.Private), fx.Invoke(register), fx.Provide(subscribe))
}

type inputs struct {
	fx.In
	Config   []settings.Instance
	Bindings []rpc.Binding `group:"xray-native-api"`
	Status   services.StatusReader
	Metadata services.MetadataReader `optional:"true"`
}

func compose(in inputs) (*services.Service, error) {
	natives := map[string]*rpc.Client{}
	for _, b := range in.Bindings {
		natives[b.ID] = b.Client
	}
	slots := make(chan struct{}, 2)
	bindings := make([]services.Binding, 0, len(in.Config))
	for _, instance := range in.Config {
		if instance.Xray == nil {
			return nil, domain.ErrInvalidArgument
		}
		b := services.Binding{ID: instance.ID, APIConfigured: instance.Xray.API != nil, ConfigurationManaged: instance.Xray.StateDirectory != ""}
		if instance.Xray.Binary != "" {
			b.Binary = process.Reader{Binary: instance.Xray.Binary, Slots: slots}
		}
		if native := natives[instance.ID]; native != nil {
			b.Native = api.New(native)
		}
		bindings = append(bindings, b)
	}
	return services.New(bindings, in.Status, in.Metadata)
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
	return natsclient.Subscriptions{Endpoints: []natsclient.Subscription{{Name: "Xray instance diagnostics", SubjectSuffix: "xray.tools.diagnostics.v1.get", Handle: h.Report}}}
}
