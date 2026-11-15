package xray

import (
	"context"
	"errors"
	"fmt"
	"io"

	"go.uber.org/fx"

	"github.com/PapilioVPN/node/internal/modules/engines/xray/app"
	instances "github.com/PapilioVPN/node/internal/modules/instances/domain"
	native "github.com/PapilioVPN/node/internal/platform/xray"
	"github.com/PapilioVPN/node/internal/settings"
)

type apiResources struct {
	readers map[instances.InstanceID]app.RuntimeInfoReader
	clients []*native.APIClient
}

func newAPIResources(config []settings.Instance) (*apiResources, error) {
	resources := &apiResources{readers: make(map[instances.InstanceID]app.RuntimeInfoReader)}
	for _, instance := range config {
		if instance.Engine != instances.Xray {
			continue
		}
		resources.readers[instance.ID] = nil
		if api := instance.Xray.API; api != nil {
			client, err := native.NewAPIClient(native.APIConfig{
				Network: api.Network, Address: api.Address, RequestTimeout: api.RequestTimeout,
				MaxReceiveBytes: api.MaxReceiveBytes, MaxConcurrent: api.MaxConcurrent,
			})
			if err != nil {
				return nil, errors.Join(fmt.Errorf("construct instance %q API: %w", instance.ID, err), resources.Close())
			}
			resources.readers[instance.ID] = client
			resources.clients = append(resources.clients, client)
		}
	}
	return resources, nil
}

func newRuntimeInfoService(resources *apiResources) *app.RuntimeInfoService {
	return app.NewRuntimeInfoService(resources.readers)
}

func registerAPILifecycle(lifecycle fx.Lifecycle, resources *apiResources) {
	lifecycle.Append(fx.Hook{OnStop: func(context.Context) error { return resources.Close() }})
}

func (r *apiResources) Close() error {
	var result error
	for i := len(r.clients) - 1; i >= 0; i-- {
		result = errors.Join(result, r.clients[i].Close())
	}
	return result
}

type apiCleanupResult struct {
	fx.Out
	Cleanup io.Closer `group:"module_resources"`
}

func apiCleanup(resources *apiResources) apiCleanupResult {
	return apiCleanupResult{Cleanup: resources}
}
