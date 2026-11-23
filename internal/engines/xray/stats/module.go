// Package stats connects Xray statistics to Node's management APIs.
package stats

import (
	"github.com/PapilioVPN/node/internal/engines/xray/instances/lifecycle"
	"github.com/PapilioVPN/node/internal/engines/xray/stats/bridge/grpc"
	"github.com/PapilioVPN/node/internal/engines/xray/stats/bridge/http"
	"github.com/PapilioVPN/node/internal/engines/xray/stats/runtime"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"
	grpcserver "google.golang.org/grpc"
)

var Module = fx.Module("xray-stats", fx.Invoke(register))

func register(life fx.Lifecycle, config settings.Xray, instance *lifecycle.Service, router *chi.Mux, server *grpcserver.Server) {
	if !config.Enabled() {
		return
	}
	service := runtime.New(instance)
	life.Append(fx.Hook{OnStop: service.Close})
	http.Register(router, service)
	grpc.Register(server, service)
}
