// Package instances composes named engine instances and their owned lifetimes.
package instances

import (
	"context"
	"errors"
	"fmt"
	"io"

	"go.uber.org/fx"
	"google.golang.org/grpc"

	"github.com/PapilioVPN/node/internal/modules/instances/app"
	"github.com/PapilioVPN/node/internal/modules/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/instances/grpcapi"
	instancesv1 "github.com/PapilioVPN/node/internal/modules/instances/grpcapi/proto/v1"
	systemdomain "github.com/PapilioVPN/node/internal/modules/system/domain"
	"github.com/PapilioVPN/node/internal/platform/xray"
	"github.com/PapilioVPN/node/internal/settings"
)

// Module composes instance operations, native adapters and capability registration.
func Module(grpcEnabled bool) fx.Option {
	options := []fx.Option{fx.Provide(newResources, newLifecycleService, capability), fx.Invoke(registerLifecycle)}
	if grpcEnabled {
		options = append(options, fx.Invoke(registerGRPC))
	}
	return fx.Module("instances", options...)
}

func registerGRPC(registrar grpc.ServiceRegistrar, reader *app.LifecycleService, cfg settings.GRPCPermissions) {
	instancesv1.RegisterInstanceServiceServer(registrar, grpcapi.Handler{
		Status: reader, AllowStatusRead: cfg.ReadInstanceStatus,
	})
}

// Resources owns final cleanup independently of Fx's startup/stop contexts.
// Construction has no native effects; registered hooks use application operations.
type Resources struct {
	instances []ownedInstance
}

type ownedInstance struct {
	id        domain.InstanceID
	lifecycle *app.Lifecycle
	closer    io.Closer
}

func newResources(config []settings.Instance) (resourceResult, error) {
	resources := &Resources{}
	for _, instance := range config {
		switch instance.Engine {
		case domain.Xray:
			process, err := xray.New(xray.Config{
				Binary: instance.Xray.Binary, ConfigFile: instance.Xray.ConfigFile,
				StopTimeout: instance.Xray.StopTimeout,
			})
			if err != nil {
				return resourceResult{}, fmt.Errorf("construct instance %q: %w", instance.ID, err)
			}
			service := app.NewLifecycle(domain.Instance{ID: instance.ID, Kind: instance.Engine}, process)
			resources.instances = append(resources.instances, ownedInstance{id: instance.ID, lifecycle: service, closer: process})
		default:
			return resourceResult{}, fmt.Errorf("%w: unsupported instance engine", settings.ErrInvalidConfig)
		}
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

func newLifecycleService(resources *Resources) *app.LifecycleService {
	bindings := make(map[domain.InstanceID]*app.Lifecycle, len(resources.instances))
	for _, instance := range resources.instances {
		bindings[instance.id] = instance.lifecycle
	}
	return app.NewLifecycleService(bindings)
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
	Value systemdomain.Capability `group:"node_capabilities"`
}

func capability() capabilityResult {
	return capabilityResult{Value: systemdomain.Capability(domain.ReadStatus)}
}

type resourceResult struct {
	fx.Out
	Resources *Resources
	Cleanup   io.Closer `group:"module_resources"`
}
