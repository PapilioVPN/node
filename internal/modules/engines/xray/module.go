// Package xray composes Xray-specific operations; instances owns runtime control.
package xray

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"go.uber.org/fx"
	"google.golang.org/grpc"

	instanceowner "github.com/PapilioVPN/node/internal/modules/core/instances"
	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/app/services"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/grpcapi"
	xrayv1 "github.com/PapilioVPN/node/internal/modules/engines/xray/grpcapi/proto/v1"
	xrayapi "github.com/PapilioVPN/node/internal/modules/engines/xray/infra/api"
	xrayprocess "github.com/PapilioVPN/node/internal/modules/engines/xray/infra/process"
	system "github.com/PapilioVPN/node/internal/modules/system/info/domain"
	"github.com/PapilioVPN/node/internal/settings"
)

// Module owns Xray observations and API clients independently of instance runtimes.
func Module(grpcEnabled bool) fx.Option {
	options := []fx.Option{
		fx.Provide(newBinaryInfoService, newAPIResources, newRuntimeInfoService, newTrafficCounterService, fx.Private),
		fx.Provide(capability, apiCleanup, newRuntimeBindings), fx.Invoke(registerAPILifecycle),
	}
	if grpcEnabled {
		options = append(options, fx.Invoke(registerGRPC))
	}
	return fx.Module("xray", options...)
}

type runtimeBindingsResult struct {
	fx.Out
	Values []instanceowner.RuntimeBinding `group:"engine_runtimes,flatten"`
}

func newRuntimeBindings(config []settings.Instance) (runtimeBindingsResult, error) {
	var result runtimeBindingsResult
	for _, instance := range config {
		if instance.Engine != instances.Xray {
			continue
		}
		runtime, err := xrayprocess.New(xrayprocess.Config{
			Binary: instance.Xray.Binary, ConfigFile: instance.Xray.ConfigFile,
			StopTimeout: instance.Xray.StopTimeout,
		})
		if err != nil {
			return runtimeBindingsResult{}, fmt.Errorf("construct instance %q: %w", instance.ID, err)
		}
		binding := instanceowner.RuntimeBinding{
			Instance: instances.Instance{ID: instance.ID, Kind: instance.Engine},
			Runtime:  runtime, Closer: runtime,
		}
		if validation := instance.Validation; validation != nil {
			validator, err := xrayprocess.NewValidator(xrayprocess.ValidationConfig{
				Binary: instance.Xray.Binary, Directory: filepath.Dir(instance.Xray.ConfigFile),
				WorkDir: validation.WorkDir, Timeout: validation.Timeout,
				MaxBytes: validation.MaxBytes, MaxConcurrent: validation.MaxConcurrent,
			})
			if err != nil {
				return runtimeBindingsResult{}, fmt.Errorf("construct instance %q validator: %w", instance.ID, err)
			}
			binding.Validator = validator
		}
		result.Values = append(result.Values, binding)
	}
	return result, nil
}

func newBinaryInfoService(config []settings.Instance) (*services.BinaryInfoService, error) {
	readers := make(map[instances.InstanceID]services.BinaryInfoReader)
	for _, instance := range config {
		if instance.Engine != instances.Xray {
			continue
		}
		readers[instance.ID] = nil
		if inspection := instance.Xray.Inspection; inspection != nil {
			reader, err := xrayprocess.NewBinaryInspector(xrayprocess.BinaryInspectionConfig{
				Binary: instance.Xray.Binary, Timeout: inspection.Timeout,
				MaxOutputBytes: inspection.MaxOutputBytes, MaxConcurrent: inspection.MaxConcurrent,
			})
			if err != nil {
				return nil, fmt.Errorf("construct instance %q binary inspector: %w", instance.ID, err)
			}
			readers[instance.ID] = reader
		}
	}
	return services.NewBinaryInfoService(readers), nil
}

type apiResources struct {
	runtimeReaders map[instances.InstanceID]services.RuntimeInfoReader
	trafficReaders map[instances.InstanceID]services.TrafficCounterReader
	clients        []*xrayapi.Client
}

func newAPIResources(config []settings.Instance) (*apiResources, error) {
	resources := &apiResources{
		runtimeReaders: make(map[instances.InstanceID]services.RuntimeInfoReader),
		trafficReaders: make(map[instances.InstanceID]services.TrafficCounterReader),
	}
	for _, instance := range config {
		if instance.Engine != instances.Xray {
			continue
		}
		resources.runtimeReaders[instance.ID] = nil
		resources.trafficReaders[instance.ID] = nil
		if api := instance.Xray.API; api != nil {
			client, err := xrayapi.NewClient(xrayapi.Config{
				Network: api.Network, Address: api.Address, RequestTimeout: api.RequestTimeout,
				MaxReceiveBytes: api.MaxReceiveBytes, MaxConcurrent: api.MaxConcurrent,
			})
			if err != nil {
				return nil, errors.Join(fmt.Errorf("construct instance %q API: %w", instance.ID, err), resources.Close())
			}
			resources.runtimeReaders[instance.ID] = client
			resources.trafficReaders[instance.ID] = client
			resources.clients = append(resources.clients, client)
		}
	}
	return resources, nil
}

func newRuntimeInfoService(resources *apiResources) *services.RuntimeInfoService {
	return services.NewRuntimeInfoService(resources.runtimeReaders)
}

func newTrafficCounterService(resources *apiResources) *services.TrafficCounterService {
	return services.NewTrafficCounterService(resources.trafficReaders)
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

func registerGRPC(registrar grpc.ServiceRegistrar, info *services.BinaryInfoService, runtime *services.RuntimeInfoService, traffic *services.TrafficCounterService, cfg settings.GRPCPermissions) {
	xrayv1.RegisterXrayServiceServer(registrar, grpcapi.Handler{
		Info: info, Runtime: runtime, Traffic: traffic,
		AllowInfoRead: cfg.ReadEngineInfo, AllowStatsRead: cfg.ReadEngineStats,
	})
}

type capabilityResult struct {
	fx.Out
	Values []system.Capability `group:"node_capabilities,flatten"`
}

func capability() capabilityResult {
	return capabilityResult{Values: []system.Capability{domain.ReadBinaryInfo, domain.ReadRuntimeInfo, domain.ReadTrafficCounter}}
}

type apiCleanupResult struct {
	fx.Out
	Cleanup io.Closer `group:"module_resources"`
}

func apiCleanup(resources *apiResources) apiCleanupResult {
	return apiCleanupResult{Cleanup: resources}
}
