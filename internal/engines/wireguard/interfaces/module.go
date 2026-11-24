// Package interfaces connects WireGuard interface status to Node.
package interfaces

import (
	"context"
	"errors"

	bridgegrpc "github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/bridge/grpc"
	bridgehttp "github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/bridge/http"
	wglifecycle "github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/lifecycle"
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
	var managed *wglifecycle.Service
	var appliedHash func(status.Interface) string
	if config.Managed {
		control := new(native.Control)
		managed = wglifecycle.New(config.Interface, control)
		appliedHash = managed.AppliedHash
		lifecycle.Append(fx.Hook{OnStart: control.Open, OnStop: func(ctx context.Context) error {
			return errors.Join(managed.Close(ctx), control.Close())
		}})
	}
	service := status.New(config.Interface, reader, appliedHash)
	validator := wglifecycle.NewValidator(config.Interface)
	bridgehttp.Register(router, service, managed, validator)
	bridgegrpc.Register(server, service, managed, validator)
}
