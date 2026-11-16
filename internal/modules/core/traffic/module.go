// Package traffic composes counter observations and resets from focused ports.
package traffic

import (
	"errors"
	"time"

	"go.uber.org/fx"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/core/traffic/app/services"
	"github.com/PapilioVPN/node/internal/modules/core/traffic/grpcapi"
	"github.com/PapilioVPN/node/internal/settings"
)

// BackendBinding supplies one configured instance's traffic operations. Timeout
// bounds each multi-call operation as a whole; nil ports retain disabled status.
type BackendBinding struct {
	InstanceID       instances.InstanceID
	Reader           services.TrafficCounterReader
	Collection       services.TrafficCountersReader
	Resetter         services.TrafficCounterResetter
	Timeout          time.Duration
	ResetConcurrency int
}

type backendBindings struct {
	fx.In
	Values []BackendBinding `group:"traffic_backends"`
}

func Module(grpcEnabled bool) fx.Option {
	options := []fx.Option{fx.Provide(newOperations, fx.Private)}
	if grpcEnabled {
		options = append(options, fx.Provide(newTrafficAPI))
	}
	return fx.Module("traffic", options...)
}

type operations struct {
	reads       *services.TrafficCounterService
	collections *services.TrafficCountersService
	resets      *services.TrafficCounterResetService
	scopeResets *services.TrafficCountersResetService
	pairs       *services.TrafficPairService
	pairResets  *services.TrafficPairResetService
}

func newOperations(bindings backendBindings) (operations, error) {
	readers := make(map[instances.InstanceID]services.TrafficCounterReader, len(bindings.Values))
	collections := make(map[instances.InstanceID]services.TrafficCountersReader, len(bindings.Values))
	resetters := make(map[instances.InstanceID]services.TrafficCounterResetter, len(bindings.Values))
	scopes := make(map[instances.InstanceID]services.TrafficResetBinding, len(bindings.Values))
	pairs := make(map[instances.InstanceID]services.TrafficPairReadBinding, len(bindings.Values))
	pairResets := make(map[instances.InstanceID]services.TrafficPairResetBinding, len(bindings.Values))
	for _, b := range bindings.Values {
		if _, duplicate := readers[b.InstanceID]; duplicate || b.InstanceID == "" {
			return operations{}, errors.New("invalid traffic backend binding")
		}
		readers[b.InstanceID], collections[b.InstanceID], resetters[b.InstanceID] = b.Reader, b.Collection, b.Resetter
		scopes[b.InstanceID] = services.TrafficResetBinding{Reader: b.Collection, Resetter: b.Resetter, Timeout: b.Timeout, MaxConcurrent: b.ResetConcurrency}
		pairs[b.InstanceID] = services.TrafficPairReadBinding{Reader: b.Reader, Timeout: b.Timeout}
		pairResets[b.InstanceID] = services.TrafficPairResetBinding{Resetter: b.Resetter, Timeout: b.Timeout}
	}
	return operations{
		reads:       services.NewTrafficCounterService(readers),
		collections: services.NewTrafficCountersService(collections),
		resets:      services.NewTrafficCounterResetService(resetters),
		scopeResets: services.NewTrafficCountersResetService(scopes),
		pairs:       services.NewTrafficPairService(pairs),
		pairResets:  services.NewTrafficPairResetService(pairResets),
	}, nil
}

func newTrafficAPI(s operations, p settings.GRPCPermissions) grpcapi.TrafficAPI {
	return grpcapi.TrafficAPI{
		Traffic: s.reads, TrafficCounters: s.collections, Resets: s.resets,
		ScopeResets: s.scopeResets, Pairs: s.pairs, PairResets: s.pairResets,
		AllowStatsRead: p.ReadEngineStats, AllowStatsReset: p.ResetEngineStats,
	}
}
