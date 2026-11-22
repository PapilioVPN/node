// Package logs composes sanitized, resumable Xray console tail and follow pulls.
package logs

import (
	"github.com/PapilioVPN/node/internal/apps/xray/observability/logs/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/logs/grpcapi"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/logs/httpapi"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/logs/infra/instances"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/logs/natsapi"
	output "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	"github.com/PapilioVPN/node/internal/platform/natsclient"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/gofiber/fiber/v3"
	"go.uber.org/fx"
	"google.golang.org/grpc"
)

func Module(requests settings.Requests) fx.Option {
	return fx.Module("xray-logs", fx.Supply(requests, fx.Private), fx.Provide(compose, fx.Private), fx.Invoke(register), fx.Provide(subscribe))
}

type inputs struct {
	fx.In
	Bindings []output.OutputBinding `group:"xray-instance-output"`
}

func compose(in inputs) (*services.Service, error) {
	bindings := make([]services.Binding, 0, len(in.Bindings))
	for _, b := range in.Bindings {
		binding := services.Binding{ID: b.ID}
		if b.Source != nil {
			binding.Reader = instances.Reader{Source: b.Source}
		}
		bindings = append(bindings, binding)
	}
	return services.New(bindings)
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
		{Name: "Xray recent console logs", SubjectSuffix: "xray.observability.logs.v1.tail", Handle: h.Tail},
		{Name: "Xray console log continuation", SubjectSuffix: "xray.observability.logs.v1.follow", Handle: h.Follow},
	}}
}
