// Package openvpn connects the OpenVPN engine to Node.
package openvpn

import (
	"github.com/PapilioVPN/node/internal/engines/openvpn/access"
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
	var revocations lifecycle.Revocations
	if config.RevocationCAFile != "" && config.RevocationListFile != "" {
		store := &access.Store{CAFile: config.RevocationCAFile, Path: config.RevocationListFile}
		driver.Revocations = store
		revocations = store
	}
	service := lifecycle.New(config.ID, config.ConfigFile, driver, revocations)
	life.Append(fx.Hook{OnStop: service.Close})
	bridgehttp.Register(router, service)
	bridgegrpc.Register(server, service)
}
