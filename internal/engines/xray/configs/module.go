// Package configs connects Xray configuration checks to Node.
package configs

import (
	"github.com/PapilioVPN/node/internal/engines/xray/configs/bridge/grpc"
	"github.com/PapilioVPN/node/internal/engines/xray/configs/bridge/http"
	"github.com/PapilioVPN/node/internal/engines/xray/configs/validation"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"
	grpcserver "google.golang.org/grpc"
)

var Module = fx.Module("xray-configs", fx.Invoke(register))

func register(config settings.Xray, router *chi.Mux, server *grpcserver.Server) {
	if !config.Enabled() {
		return
	}
	service := validation.New(validation.Config(config))
	http.Register(router, service)
	grpc.Register(server, service)
}
