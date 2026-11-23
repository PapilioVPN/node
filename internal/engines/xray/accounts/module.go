// Package accounts exposes live Xray inbound account operations to Node management clients.
package accounts

import (
	"context"

	"github.com/PapilioVPN/node/internal/engines/xray/accounts/bridge/grpc"
	"github.com/PapilioVPN/node/internal/engines/xray/accounts/bridge/http"
	"github.com/PapilioVPN/node/internal/engines/xray/accounts/membership"
	"github.com/PapilioVPN/node/internal/engines/xray/accounts/native"
	"github.com/PapilioVPN/node/internal/engines/xray/instances/lifecycle"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"
	grpcserver "google.golang.org/grpc"
)

var Module = fx.Module("xray-accounts", fx.Invoke(register))

func register(config settings.Xray, instances *lifecycle.Service, router *chi.Mux, server *grpcserver.Server) {
	if !config.Enabled() {
		return
	}
	client := native.New(instances)
	// Keep composition compatible with the lifecycle branch until integration.
	type initializer = func(context.Context, string, *grpcserver.ClientConn, []membership.InboundSnapshot, []string) (func(), error)
	if owner, ok := any(instances).(interface{ SetAccountInitializer(initializer) }); ok {
		owner.SetAccountInitializer(client.Initialize)
	}
	service := membership.New(client)
	http.Register(router, service)
	grpc.Register(server, service)
}
