// Package instances composes named engine instances and their owned lifetimes.
package instances

import (
	"context"
	"errors"
	"fmt"
	"io"

	"go.uber.org/fx"
	"google.golang.org/grpc"

	"github.com/PapilioVPN/node/internal/apps/core/engines/instances/app/services"
	"github.com/PapilioVPN/node/internal/apps/core/engines/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/core/engines/instances/grpcapi"
	instancesv1 "github.com/PapilioVPN/node/internal/apps/core/engines/instances/grpcapi/proto/v1"
	systemdomain "github.com/PapilioVPN/node/internal/apps/system/node/info/domain"
	"github.com/PapilioVPN/node/internal/settings"
)

// Module composes instance operations, native adapters and capability registration.
func Module(grpcEnabled bool) fx.Option {
	options := []fx.Option{fx.Provide(newResources, newLifecycleService, newConfigService, capability), fx.Invoke(registerLifecycle)}
	if grpcEnabled {
		options = append(options, fx.Invoke(registerGRPC))
	}
	return fx.Module("instances", options...)
}

func registerGRPC(registrar grpc.ServiceRegistrar, service *services.LifecycleService, configs *services.ConfigService, cfg settings.GRPCPermissions) {
	instancesv1.RegisterInstanceServiceServer(registrar, grpcapi.Handler{
		Status: service, Statuses: service, Control: service,
		AllowStatusRead: cfg.ReadInstanceStatus, AllowControl: cfg.ControlInstances,
		Configs: configs, AllowConfigValidation: cfg.ValidateInstanceConfig,
	})
}

// Resources owns final cleanup independently of Fx's startup/stop contexts.
// Construction has no native effects; registered hooks use application operations.
type Resources struct {
	instances []ownedInstance
}

type ownedInstance struct {
	id        domain.InstanceID
	lifecycle *services.Lifecycle
	closer    io.Closer
	validator services.ConfigValidator
}

// RuntimeBinding contributes an effect-free engine adapter to instance ownership.
// Core controls startup/stop order and final Close; Validator may be absent.
type RuntimeBinding struct {
	Instance  domain.Instance
	Runtime   services.Runtime
	Closer    io.Closer
	Validator services.ConfigValidator
}

type runtimeBindings struct {
	fx.In
	Values []RuntimeBinding `group:"engine_runtimes"`
}

func newResources(config []settings.Instance, bindings runtimeBindings) (resourceResult, error) {
	if len(config) != len(bindings.Values) {
		return resourceResult{}, errors.New("configured instances and engine runtime bindings differ")
	}
	byID := make(map[domain.InstanceID]RuntimeBinding, len(bindings.Values))
	for _, binding := range bindings.Values {
		if _, duplicate := byID[binding.Instance.ID]; duplicate || binding.Runtime == nil || binding.Closer == nil {
			return resourceResult{}, fmt.Errorf("invalid runtime binding for instance %q", binding.Instance.ID)
		}
		byID[binding.Instance.ID] = binding
	}
	resources := &Resources{}
	// Fx groups have no ordering guarantee; the configured order remains authoritative.
	for _, instance := range config {
		binding, exists := byID[instance.ID]
		if !exists || binding.Instance.Kind != instance.Engine {
			return resourceResult{}, fmt.Errorf("missing matching runtime binding for instance %q", instance.ID)
		}
		resources.instances = append(resources.instances, ownedInstance{
			id: instance.ID, lifecycle: services.NewLifecycle(binding.Instance, binding.Runtime),
			closer: binding.Closer, validator: binding.Validator,
		})
	}
	return resourceResult{Resources: resources, Cleanup: resources}, nil
}

func registerLifecycle(lifecycle fx.Lifecycle, resources *Resources) {
	for _, instance := range resources.instances {
		lifecycle.Append(fx.Hook{
			OnStart: func(ctx context.Context) error {
				_, err := instance.lifecycle.Start(ctx)
				if err != nil {
					return fmt.Errorf("start instance %q: %w", instance.id, err)
				}
				return nil
			},
			OnStop: func(ctx context.Context) error {
				_, err := instance.lifecycle.Stop(ctx)
				if err != nil {
					return fmt.Errorf("stop instance %q: %w", instance.id, err)
				}
				return nil
			},
		})
	}
}

func newLifecycleService(resources *Resources) *services.LifecycleService {
	bindings := make(map[domain.InstanceID]*services.Lifecycle, len(resources.instances))
	for _, instance := range resources.instances {
		bindings[instance.id] = instance.lifecycle
	}
	return services.NewLifecycleService(bindings)
}

func newConfigService(resources *Resources) *services.ConfigService {
	bindings := make(map[domain.InstanceID]services.ConfigValidator, len(resources.instances))
	for _, instance := range resources.instances {
		bindings[instance.id] = instance.validator
	}
	return services.NewConfigService(bindings)
}

// Close permanently stops every owned instance in reverse construction order.
// It also covers instances whose OnStop hook Fx skipped after cancellation.
// Joining cleanup can outlive Fx's budget, bounded by each process's stop timeout.
func (r *Resources) Close() error {
	var result error
	for i := len(r.instances) - 1; i >= 0; i-- {
		instance := r.instances[i]
		if err := instance.closer.Close(); err != nil {
			result = errors.Join(result, fmt.Errorf("close instance %q: %w", instance.id, err))
		}
	}
	return result
}

type capabilityResult struct {
	fx.Out
	Values []systemdomain.Capability `group:"node_capabilities,flatten"`
}

func capability() capabilityResult {
	return capabilityResult{Values: []systemdomain.Capability{
		domain.ReadStatus, domain.ListStatuses, domain.StartInstance, domain.StopInstance,
		domain.ValidateConfig,
	}}
}

type resourceResult struct {
	fx.Out
	Resources *Resources
	Cleanup   io.Closer `group:"module_resources"`
}
