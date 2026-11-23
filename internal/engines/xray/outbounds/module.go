// Package outbounds connects recorded Xray outbound health to Node's management APIs.
package outbounds

import (
	"github.com/PapilioVPN/node/internal/engines/xray/instances/lifecycle"
	"github.com/PapilioVPN/node/internal/engines/xray/outbounds/bridge/grpc"
	"github.com/PapilioVPN/node/internal/engines/xray/outbounds/bridge/http"
	"github.com/PapilioVPN/node/internal/engines/xray/outbounds/health"
	"github.com/PapilioVPN/node/internal/engines/xray/outbounds/native"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"
	grpcserver "google.golang.org/grpc"
)

var Module = fx.Module("xray-outbounds", fx.Invoke(register))

func register(config settings.Xray, instances *lifecycle.Service, router *chi.Mux, server *grpcserver.Server) {
	if !config.Enabled() {
		return
	}
	service := health.New(native.NewObservatory(instances))
	http.Register(router, service)
	grpc.Register(server, service)
}
