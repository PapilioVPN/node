// Package xray composes Xray-specific operations; instances owns runtime control.
package xray

import (
	"context"
	"errors"
	"fmt"
	"io"

	"go.uber.org/fx"
	"google.golang.org/grpc"

	"github.com/PapilioVPN/node/internal/modules/engines/xray/app"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/grpcapi"
	xrayv1 "github.com/PapilioVPN/node/internal/modules/engines/xray/grpcapi/proto/v1"
	instances "github.com/PapilioVPN/node/internal/modules/instances/domain"
	system "github.com/PapilioVPN/node/internal/modules/system/domain"
	native "github.com/PapilioVPN/node/internal/platform/xray"
	"github.com/PapilioVPN/node/internal/settings"
)

// Module owns Xray observations and API clients independently of instance runtimes.
func Module(grpcEnabled bool) fx.Option {
	options := []fx.Option{
		fx.Provide(newBinaryInfoService, newAPIResources, newRuntimeInfoService, newTrafficCounterService, fx.Private),
		fx.Provide(capability, apiCleanup), fx.Invoke(registerAPILifecycle),
	}
	if grpcEnabled {
		options = append(options, fx.Invoke(registerGRPC))
	}
	return fx.Module("xray", options...)
}

func newBinaryInfoService(config []settings.Instance) (*app.BinaryInfoService, error) {
	readers := make(map[instances.InstanceID]app.BinaryInfoReader)
	for _, instance := range config {
		if instance.Engine != instances.Xray {
			continue
		}
		readers[instance.ID] = nil
		if inspection := instance.Xray.Inspection; inspection != nil {
			reader, err := native.NewBinaryInspector(native.BinaryInspectionConfig{
				Binary: instance.Xray.Binary, Timeout: inspection.Timeout,
				MaxOutputBytes: inspection.MaxOutputBytes, MaxConcurrent: inspection.MaxConcurrent,
			})
			if err != nil {
				return nil, fmt.Errorf("construct instance %q binary inspector: %w", instance.ID, err)
			}
			readers[instance.ID] = reader
		}
	}
	return app.NewBinaryInfoService(readers), nil
}

type apiResources struct {
	runtimeReaders map[instances.InstanceID]app.RuntimeInfoReader
	trafficReaders map[instances.InstanceID]app.TrafficCounterReader
	clients        []*native.APIClient
}

func newAPIResources(config []settings.Instance) (*apiResources, error) {
	resources := &apiResources{
		runtimeReaders: make(map[instances.InstanceID]app.RuntimeInfoReader),
		trafficReaders: make(map[instances.InstanceID]app.TrafficCounterReader),
	}
	for _, instance := range config {
		if instance.Engine != instances.Xray {
			continue
		}
		resources.runtimeReaders[instance.ID] = nil
		resources.trafficReaders[instance.ID] = nil
		if api := instance.Xray.API; api != nil {
			client, err := native.NewAPIClient(native.APIConfig{
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

func newRuntimeInfoService(resources *apiResources) *app.RuntimeInfoService {
	return app.NewRuntimeInfoService(resources.runtimeReaders)
}

func newTrafficCounterService(resources *apiResources) *app.TrafficCounterService {
	return app.NewTrafficCounterService(resources.trafficReaders)
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

func registerGRPC(registrar grpc.ServiceRegistrar, info *app.BinaryInfoService, runtime *app.RuntimeInfoService, traffic *app.TrafficCounterService, cfg settings.GRPCPermissions) {
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
