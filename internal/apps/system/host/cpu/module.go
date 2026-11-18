// Package cpu composes host processor observations and their adapters.
package cpu

import (
	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/grpcapi"
	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/httpapi"
	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/infra/sys"
	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/natsapi"
	"github.com/PapilioVPN/node/internal/platform/natsclient"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/gofiber/fiber/v3"
	"go.uber.org/fx"
	"google.golang.org/grpc"
)

func Module(requests settings.Requests) fx.Option {
	return fx.Module("cpu", fx.Supply(requests, fx.Private),
		fx.Provide(func() services.Service {
			return services.Service{Reader: sys.Reader{}, TimesReader: sys.TimesReader{}, InfoReader: sys.InfoReader{}, FrequencyReader: sys.FrequencyReader{}}
		}, fx.Private),
		fx.Invoke(register), fx.Provide(subscribe),
	)
}

type adapters struct {
	fx.In
	Service  services.Service
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
	return natsclient.Subscriptions{Endpoints: []natsclient.Subscription{{
		Name: "host load", SubjectSuffix: "system.host.load.v1.get", Handle: handler.Read,
	}, {
		Name: "host CPU times", SubjectSuffix: "system.host.cpu.times.v1.get", Handle: handler.ReadTimes,
	}, {
		Name: "host CPU usage", SubjectSuffix: "system.host.cpu.usage.v1.get", Handle: handler.SampleUsage,
	}, {
		Name: "host CPU frequency", SubjectSuffix: "system.host.cpu.frequency.v1.get", Handle: handler.ReadFrequency,
	}, {
		Name: "host CPU info", SubjectSuffix: "system.host.cpu.info.v1.get", Handle: handler.ReadInfo,
	}}}
}
