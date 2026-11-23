// Package logs connects live Xray execution output to Node's management APIs.
package logs

import (
	logsgrpc "github.com/PapilioVPN/node/internal/engines/xray/logs/bridge/grpc"
	logshttp "github.com/PapilioVPN/node/internal/engines/xray/logs/bridge/http"
	"github.com/PapilioVPN/node/internal/engines/xray/logs/live"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"
	"google.golang.org/grpc"
)

var Module = fx.Module("xray-logs", fx.Provide(newService), fx.Invoke(register))

func newService(config settings.Xray) *live.Service { return live.New(config.ID) }

func register(config settings.Xray, service *live.Service, router *chi.Mux, server *grpc.Server) {
	if config.Enabled() {
		logshttp.Register(router, service)
		logsgrpc.Register(server, service)
	}
}
