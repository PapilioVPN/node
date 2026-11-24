// Package interfaces connects WireGuard interface status to Node.
package interfaces

import (
	bridgegrpc "github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/bridge/grpc"
	bridgehttp "github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/bridge/http"
	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/native"
	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/status"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"
	"google.golang.org/grpc"
)

var Module = fx.Module("wireguard-interfaces", fx.Invoke(register))

func register(lifecycle fx.Lifecycle, config settings.WireGuard, router *chi.Mux, server *grpc.Server) {
	if !config.Enabled() {
		return
	}
	reader := new(native.Reader)
	lifecycle.Append(fx.Hook{OnStart: reader.Open, OnStop: reader.Close})
	service := status.New(config.Interface, reader)
	bridgehttp.Register(router, service)
	bridgegrpc.Register(server, service)
}
