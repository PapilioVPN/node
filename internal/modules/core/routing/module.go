// Package routing composes route observations, probes and mutations from focused ports.
package routing

import (
	"errors"

	"go.uber.org/fx"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/core/routing/app/services"
	"github.com/PapilioVPN/node/internal/modules/core/routing/grpcapi"
	"github.com/PapilioVPN/node/internal/settings"
)

// BackendBinding supplies the routing capabilities of one configured instance.
// Nil ports retain explicit disabled/unsupported operations without another resource.
type BackendBinding struct {
	InstanceID instances.InstanceID
	Reader     services.RoutingReader
	Prober     services.RoutingProber
	Overrider  services.BalancerOverrider
	Remover    services.RoutingRuleRemover
	Addition   services.RoutingRuleBinding
}

type backendBindings struct {
	fx.In
	Values []BackendBinding `group:"routing_backends"`
}

func Module(grpcEnabled bool) fx.Option {
	options := []fx.Option{fx.Provide(newOperations, fx.Private)}
	if grpcEnabled {
		options = append(options, fx.Provide(newRoutingAPI))
	}
	return fx.Module("routing", options...)
}

type operations struct {
	reads     *services.RoutingService
	probes    *services.RoutingProbeService
	overrides *services.BalancerOverrideService
	removals  *services.RoutingRuleRemovalService
	additions *services.RoutingRuleAdditionService
}

func newOperations(bindings backendBindings) (operations, error) {
	readers := make(map[instances.InstanceID]services.RoutingReader, len(bindings.Values))
	probers := make(map[instances.InstanceID]services.RoutingProber, len(bindings.Values))
	overriders := make(map[instances.InstanceID]services.BalancerOverrider, len(bindings.Values))
	removers := make(map[instances.InstanceID]services.RoutingRuleRemover, len(bindings.Values))
	additions := make(map[instances.InstanceID]services.RoutingRuleBinding, len(bindings.Values))
	for _, b := range bindings.Values {
		if _, duplicate := readers[b.InstanceID]; duplicate || b.InstanceID == "" {
			return operations{}, errors.New("invalid routing backend binding")
		}
		readers[b.InstanceID] = b.Reader
		probers[b.InstanceID] = b.Prober
		overriders[b.InstanceID] = b.Overrider
		removers[b.InstanceID] = b.Remover
		additions[b.InstanceID] = b.Addition
	}
	return operations{
		reads:     services.NewRoutingService(readers),
		probes:    services.NewRoutingProbeService(probers),
		overrides: services.NewBalancerOverrideService(overriders),
		removals:  services.NewRoutingRuleRemovalService(removers),
		additions: services.NewRoutingRuleAdditionService(additions),
	}, nil
}

func newRoutingAPI(s operations, p settings.GRPCPermissions) grpcapi.RoutingAPI {
	return grpcapi.RoutingAPI{
		Routing: s.reads, Probes: s.probes, BalancerOverrides: s.overrides,
		RuleRemoval: s.removals, RuleAddition: s.additions,
		AllowRoutingRead:       p.ReadEngineRouting,
		AllowRoutingTest:       p.TestEngineRouting,
		AllowRoutingManagement: p.ManageEngineRouting,
	}
}
