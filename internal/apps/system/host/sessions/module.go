// Package sessions composes native session observations and management adapters.
package sessions

import (
	"github.com/PapilioVPN/node/internal/apps/system/host/sessions/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/host/sessions/grpcapi"
	"github.com/PapilioVPN/node/internal/apps/system/host/sessions/httpapi"
	"github.com/PapilioVPN/node/internal/apps/system/host/sessions/infra/logind"
	"github.com/PapilioVPN/node/internal/apps/system/host/sessions/infra/utmp"
	"github.com/PapilioVPN/node/internal/apps/system/host/sessions/natsapi"
	"github.com/PapilioVPN/node/internal/platform/natsclient"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/gofiber/fiber/v3"
	"go.uber.org/fx"
	"google.golang.org/grpc"
)

func Module(requests settings.Requests, sources settings.Sessions) fx.Option {
	sources = sources.Defaults()
	return fx.Module("sessions", fx.Supply(requests, fx.Private), fx.Provide(func() services.Service {
		return services.Service{Logind: logind.Reader{SocketPath: sources.BusSocketPath, Timeout: requests.Timeout}, Records: utmp.Reader{Path: sources.UtmpPath}}
	}, fx.Private), fx.Invoke(register), fx.Provide(subscribe))
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
	return natsclient.Subscriptions{Endpoints: []natsclient.Subscription{{Name: "host sessions", SubjectSuffix: "system.host.sessions.v1.list", Handle: handler.List}, {Name: "host session details", SubjectSuffix: "system.host.sessions.v1.get", Handle: handler.Read}, {Name: "host login records", SubjectSuffix: "system.host.login_records.v1.list", Handle: handler.ListRecords}}}
}
