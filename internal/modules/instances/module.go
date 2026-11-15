// Package instances composes named engine instances and their owned lifetimes.
package instances

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/fx"

	"github.com/PapilioVPN/node/internal/engines/xray"
	"github.com/PapilioVPN/node/internal/modules/instances/app"
	"github.com/PapilioVPN/node/internal/modules/instances/domain"
	"github.com/PapilioVPN/node/internal/settings"
)

var Module = fx.Module("instances", fx.Provide(newResources), fx.Invoke(registerLifecycle))

// Resources owns final cleanup independently of Fx's startup/stop contexts.
// Construction has no native effects; registered hooks use application operations.
type Resources struct {
	instances []ownedInstance
}

type ownedInstance struct {
	id        domain.InstanceID
	lifecycle *app.Lifecycle
	process   *xray.Process
}

func newResources(cfg settings.Config) (*Resources, error) {
	resources := &Resources{}
	for _, instance := range cfg.Instances {
		switch instance.Engine {
		case domain.Xray:
			process, err := xray.New(xray.Config{
				Binary: instance.Xray.Binary, ConfigFile: instance.Xray.ConfigFile,
				StopTimeout: instance.Xray.StopTimeout,
			})
			if err != nil {
				return nil, fmt.Errorf("construct instance %q: %w", instance.ID, err)
			}
			service := app.NewLifecycle(domain.Instance{ID: instance.ID, Kind: instance.Engine}, process)
			resources.instances = append(resources.instances, ownedInstance{instance.ID, service, process})
		default:
			return nil, fmt.Errorf("%w: unsupported instance engine", settings.ErrInvalidConfig)
		}
	}
	return resources, nil
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

// Close permanently stops every owned instance in reverse construction order.
// It also covers instances whose OnStop hook Fx skipped after cancellation.
// Joining cleanup can outlive Fx's budget, bounded by each process's stop timeout.
func (r *Resources) Close() error {
	var result error
	for i := len(r.instances) - 1; i >= 0; i-- {
		instance := r.instances[i]
		if err := instance.process.Close(); err != nil {
			result = errors.Join(result, fmt.Errorf("close instance %q: %w", instance.id, err))
		}
	}
	return result
}
