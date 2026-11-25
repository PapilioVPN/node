// Package interfaces connects managed WireGuard interfaces to Node.
package interfaces

import (
	"context"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/engines/wireguard/diagnostics/events"
	"github.com/PapilioVPN/node/internal/engines/wireguard/diagnostics/probe"
	"github.com/PapilioVPN/node/internal/engines/wireguard/diagnostics/process"

	bridgegrpc "github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/bridge/grpc"
	bridgehttp "github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/bridge/http"
	wglifecycle "github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/lifecycle"
	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/native"
	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/peers"
	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/status"
	"github.com/PapilioVPN/node/internal/engines/wireguard/routing"
	"github.com/PapilioVPN/node/internal/engines/wireguard/stats/accounting"
	"github.com/PapilioVPN/node/internal/engines/wireguard/stats/traffic"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"
	"google.golang.org/grpc"
)

var Module = fx.Module("wireguard-interfaces", fx.Invoke(register))

func register(lifecycle fx.Lifecycle, config settings.WireGuard, router *chi.Mux, server *grpc.Server, peerStats *traffic.Service, usage *accounting.Service, logs *events.Service, runtime *process.Runtime, egress *probe.Service, routeService *routing.Service) error {
	if !config.Enabled() {
		return nil
	}
	key, err := native.ReadDefaultPeerKey(config.PeerPresharedKeyFile)
	if err != nil {
		return err
	}
	reader := new(native.Reader)
	lifecycle.Append(fx.Hook{OnStart: reader.Open, OnStop: reader.Close})
	var managed *wglifecycle.Service
	var appliedHash func(status.Interface) string
	var readLease func(status.Interface) time.Time
	if config.Managed {
		control := new(native.Control)
		var store wglifecycle.CheckpointStore
		if config.StateDirectory != "" {
			checkpoints := &native.CheckpointStore{Path: config.StateDirectory}
			store = checkpoints
			lifecycle.Append(fx.Hook{OnStart: checkpoints.Open, OnStop: func(context.Context) error {
				return checkpoints.Close()
			}})
		}
		managed = wglifecycle.New(config.Interface, control, control, store).WithPeerTraffic(peerStats).WithPeerUsage(usage).WithEvents(logs.Publish).WithConfiguration(control.UpdateConfiguration).WithRepair(control.Repair)
		managed.WithPeerPresharedKey(key)
		if routeService.Enabled() {
			managed.WithRouting(routeService)
		}
		runtime.Bind(managed.RuntimeSince)
		egress.Bind(managed.CheckApplied)
		appliedHash = managed.AppliedHash
		readLease = managed.LeaseExpiresAt
		lifecycle.Append(fx.Hook{
			OnStart: func(ctx context.Context) error {
				if err := control.Open(ctx); err != nil {
					return err
				}
				if err := managed.Recover(ctx); err != nil {
					return errors.Join(err, control.Close())
				}
				return nil
			},
			OnStop: func(ctx context.Context) error {
				return errors.Join(managed.Close(ctx), control.Close())
			},
		})
	}
	deviceReader := new(native.PeerStatsReader)
	lifecycle.Append(fx.Hook{OnStop: deviceReader.Close})
	service := status.New(config.Interface, reader, appliedHash, func(ctx context.Context, link status.Interface) (status.Configuration, error) {
		return reader.ReadConfiguration(ctx, link, deviceReader)
	}).WithObservation(native.Versions(), readLease)
	membership := peers.NewMembership(config.Interface, deviceReader)
	validator := wglifecycle.NewValidator(config.Interface)
	bridgehttp.Register(router, service, peerStats, managed, validator, membership)
	bridgegrpc.Register(server, service, peerStats, managed, validator, membership)
	return nil
}
