// Package routing composes route observations, probes and mutations from focused ports.
package routing

import (
	"errors"
	"fmt"
	"path/filepath"

	"go.uber.org/fx"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	system "github.com/PapilioVPN/node/internal/modules/system/info/domain"
	"github.com/PapilioVPN/node/internal/modules/xray/routing/app/services"
	"github.com/PapilioVPN/node/internal/modules/xray/routing/domain"
	"github.com/PapilioVPN/node/internal/modules/xray/routing/grpcapi"
	routingapi "github.com/PapilioVPN/node/internal/modules/xray/routing/infra/api"
	routingprocess "github.com/PapilioVPN/node/internal/modules/xray/routing/infra/process"
	nativeapi "github.com/PapilioVPN/node/internal/platform/xray"
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
	options := []fx.Option{fx.Provide(newNativeBindings, capability), fx.Provide(newOperations, fx.Private)}
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

type capabilityResult struct {
	fx.Out
	Values []system.Capability `group:"node_capabilities,flatten"`
}

func capability() capabilityResult {
	return capabilityResult{Values: []system.Capability{domain.ListRoutingRules, domain.ReadBalancerInfo, domain.TestRoute, domain.OverrideBalancerTarget, domain.RemoveRoutingRule, domain.AddRoutingRule}}
}

type nativeBindings struct {
	fx.Out
	Values []BackendBinding `group:"routing_backends,flatten"`
}

func newNativeBindings(config []settings.Instance, resources *nativeapi.Resources) (nativeBindings, error) {
	result := nativeBindings{}
	for _, instance := range config {
		if instance.Engine != instances.Xray {
			continue
		}
		client, exists := resources.Client(string(instance.ID))
		if !exists {
			return nativeBindings{}, fmt.Errorf("missing Xray API binding for instance %q", instance.ID)
		}
		binding := BackendBinding{InstanceID: instance.ID}
		if client != nil {
			router := &routingapi.Router{API: client}
			binding.Reader, binding.Prober, binding.Overrider, binding.Remover = router, router, router, router
			binding.Addition = services.RoutingRuleBinding{Applier: router, Timeout: instance.Xray.API.RequestTimeout}
			if rules := instance.Xray.RoutingRules; rules != nil {
				preparer, err := routingprocess.NewRoutingRulePreparer(routingprocess.RoutingRulePreparationConfig{Binary: instance.Xray.Binary, Directory: filepath.Dir(instance.Xray.ConfigFile), WorkDir: rules.WorkDir, Timeout: instance.Xray.API.RequestTimeout, MaxInputBytes: rules.MaxInputBytes, MaxOutputBytes: rules.MaxOutputBytes, MaxConcurrent: rules.MaxConcurrent})
				if err != nil {
					return nativeBindings{}, fmt.Errorf("construct instance %q routing preparer: %w", instance.ID, err)
				}
				binding.Addition.Preparer = preparer
			}
		}
		result.Values = append(result.Values, binding)
	}
	return result, nil
}
