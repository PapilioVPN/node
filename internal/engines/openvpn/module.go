// Package openvpn connects the OpenVPN engine to Node.
package openvpn

import (
	bridgegrpc "github.com/PapilioVPN/node/internal/engines/openvpn/instances/bridge/grpc"
	bridgehttp "github.com/PapilioVPN/node/internal/engines/openvpn/instances/bridge/http"
	"github.com/PapilioVPN/node/internal/engines/openvpn/instances/lifecycle"
	"github.com/PapilioVPN/node/internal/engines/openvpn/instances/native"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"
	"google.golang.org/grpc"
)

var Module = fx.Module("openvpn", fx.Invoke(register))

func register(life fx.Lifecycle, config settings.OpenVPN, router *chi.Mux, server *grpc.Server) {
	if !config.Enabled() {
		return
	}
	driver := native.Driver{
		Executable: config.Executable, ConfigFile: config.ConfigFile, RuntimeDirectory: config.RuntimeDirectory,
	}
	service := lifecycle.New(config.ID, config.ConfigFile, driver)
	life.Append(fx.Hook{OnStop: service.Close})
	bridgehttp.Register(router, service)
	bridgegrpc.Register(server, service)
}
