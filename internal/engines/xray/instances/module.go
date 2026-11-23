// Package instances connects the Xray instance lifecycle to Node.
package instances

import (
	"github.com/PapilioVPN/node/internal/engines/xray/instances/bridge/grpc"
	"github.com/PapilioVPN/node/internal/engines/xray/instances/bridge/http"
	"github.com/PapilioVPN/node/internal/engines/xray/instances/lifecycle"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"
	grpcserver "google.golang.org/grpc"
)

var Module = fx.Module("xray-instances", fx.Provide(newService), fx.Invoke(register))

func newService(config settings.Xray) *lifecycle.Service {
	return lifecycle.New(lifecycle.Config(config))
}

func register(life fx.Lifecycle, config settings.Xray, service *lifecycle.Service, router *chi.Mux, server *grpcserver.Server) {
	if !config.Enabled() {
		return
	}
	life.Append(fx.Hook{OnStop: service.Close})
	http.Register(router, service)
	grpc.Register(server, service)
}
