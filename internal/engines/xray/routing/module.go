// Package routing connects Xray routing state to Node's management APIs.
package routing

import (
	"github.com/PapilioVPN/node/internal/engines/xray/instances/lifecycle"
	"github.com/PapilioVPN/node/internal/engines/xray/routing/bridge/grpc"
	"github.com/PapilioVPN/node/internal/engines/xray/routing/bridge/http"
	"github.com/PapilioVPN/node/internal/engines/xray/routing/native"
	"github.com/PapilioVPN/node/internal/engines/xray/routing/state"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"
	grpcserver "google.golang.org/grpc"
)

var Module = fx.Module("xray-routing", fx.Invoke(register))

func register(config settings.Xray, instance *lifecycle.Service, router *chi.Mux, server *grpcserver.Server) {
	if !config.Enabled() {
		return
	}
	service := state.New(native.New(instance))
	http.Register(router, service)
	grpc.Register(server, service)
}
