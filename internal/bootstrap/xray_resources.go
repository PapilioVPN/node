package bootstrap

import (
	"context"
	"io"

	"go.uber.org/fx"

	"github.com/PapilioVPN/node/internal/bootstrap/xrayapi"
	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	nativeapi "github.com/PapilioVPN/node/internal/platform/xray"
	"github.com/PapilioVPN/node/internal/settings"
)

func xrayServices(grpcEnabled bool) fx.Option {
	options := []fx.Option{fx.Provide(newXrayAPIs), fx.Invoke(registerXrayAPILifecycle)}
	if grpcEnabled {
		options = append(options, fx.Invoke(xrayapi.Register))
	}
	return fx.Options(options...)
}

type xrayAPIResult struct {
	fx.Out
	Resources *nativeapi.Resources
	Cleanup   io.Closer `group:"module_resources"`
}

func newXrayAPIs(config []settings.Instance) (xrayAPIResult, error) {
	values := make([]nativeapi.InstanceConfig, 0, len(config))
	for _, instance := range config {
		if instance.Engine != instances.Xray {
			continue
		}
		value := nativeapi.InstanceConfig{ID: string(instance.ID)}
		if api := instance.Xray.API; api != nil {
			value.API = &nativeapi.Config{Network: api.Network, Address: api.Address, RequestTimeout: api.RequestTimeout, MaxReceiveBytes: api.MaxReceiveBytes, MaxConcurrent: api.MaxConcurrent}
		}
		values = append(values, value)
	}
	resources, err := nativeapi.NewResources(values)
	if err != nil {
		return xrayAPIResult{}, err
	}
	return xrayAPIResult{Resources: resources, Cleanup: resources}, nil
}

func registerXrayAPILifecycle(lifecycle fx.Lifecycle, resources *nativeapi.Resources) {
	lifecycle.Append(fx.Hook{OnStop: func(context.Context) error { return resources.Close() }})
}
