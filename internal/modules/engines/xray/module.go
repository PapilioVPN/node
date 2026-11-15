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

// Module owns Xray operations and API clients independently of instance runtimes.
func Module(grpcEnabled bool) fx.Option {
	options := []fx.Option{
		fx.Provide(newBinaryInfoService, newAPIResources, newRuntimeInfoService, newTrafficCounterService, newTrafficCounterResetService, newTrafficCountersResetService, newTrafficCountersService, newTrafficPairServices, newOnlineService, fx.Private),
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
	runtimeReaders    map[instances.InstanceID]services.RuntimeInfoReader
	trafficReaders    map[instances.InstanceID]services.TrafficCounterReader
	trafficResetters  map[instances.InstanceID]services.TrafficCounterResetter
	collectionReaders map[instances.InstanceID]services.TrafficCountersReader
	onlineReaders     map[instances.InstanceID]services.OnlineReader
	clients           []*xrayapi.Client
}

func newAPIResources(config []settings.Instance) (*apiResources, error) {
	resources := &apiResources{
		runtimeReaders:    make(map[instances.InstanceID]services.RuntimeInfoReader),
		trafficReaders:    make(map[instances.InstanceID]services.TrafficCounterReader),
		trafficResetters:  make(map[instances.InstanceID]services.TrafficCounterResetter),
		collectionReaders: make(map[instances.InstanceID]services.TrafficCountersReader),
		onlineReaders:     make(map[instances.InstanceID]services.OnlineReader),
	}
	for _, instance := range config {
		if instance.Engine != instances.Xray {
			continue
		}
		resources.runtimeReaders[instance.ID] = nil
		resources.trafficReaders[instance.ID] = nil
		resources.trafficResetters[instance.ID] = nil
		resources.collectionReaders[instance.ID] = nil
		resources.onlineReaders[instance.ID] = nil
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
			resources.trafficResetters[instance.ID] = client
			resources.collectionReaders[instance.ID] = client
			resources.onlineReaders[instance.ID] = client
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

func newTrafficCounterResetService(resources *apiResources) *services.TrafficCounterResetService {
	return services.NewTrafficCounterResetService(resources.trafficResetters)
}

func newTrafficCountersService(resources *apiResources) *services.TrafficCountersService {
	return services.NewTrafficCountersService(resources.collectionReaders)
}

func newTrafficCountersResetService(config []settings.Instance, resources *apiResources) *services.TrafficCountersResetService {
	bindings := make(map[instances.InstanceID]services.TrafficResetBinding)
	for _, instance := range config {
		if instance.Engine != instances.Xray {
			continue
		}
		binding := services.TrafficResetBinding{Reader: resources.collectionReaders[instance.ID], Resetter: resources.trafficResetters[instance.ID]}
		if api := instance.Xray.API; api != nil {
			binding.Timeout = api.RequestTimeout
		}
		bindings[instance.ID] = binding
	}
	return services.NewTrafficCountersResetService(bindings)
}

func newOnlineService(resources *apiResources) *services.OnlineService {
	return services.NewOnlineService(resources.onlineReaders)
}

func newTrafficPairServices(config []settings.Instance, resources *apiResources) (*services.TrafficPairService, *services.TrafficPairResetService) {
	readers := make(map[instances.InstanceID]services.TrafficPairReadBinding)
	resetters := make(map[instances.InstanceID]services.TrafficPairResetBinding)
	for _, instance := range config {
		if instance.Engine != instances.Xray {
			continue
		}
		reader := services.TrafficPairReadBinding{Reader: resources.trafficReaders[instance.ID]}
		resetter := services.TrafficPairResetBinding{Resetter: resources.trafficResetters[instance.ID]}
		if api := instance.Xray.API; api != nil {
			reader.Timeout, resetter.Timeout = api.RequestTimeout, api.RequestTimeout
		}
		readers[instance.ID], resetters[instance.ID] = reader, resetter
	}
	return services.NewTrafficPairService(readers), services.NewTrafficPairResetService(resetters)
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

type grpcServices struct {
	fx.In
	Registrar   grpc.ServiceRegistrar
	Info        *services.BinaryInfoService
	Runtime     *services.RuntimeInfoService
	Traffic     *services.TrafficCounterService
	Resets      *services.TrafficCounterResetService
	ScopeResets *services.TrafficCountersResetService
	Pairs       *services.TrafficPairService
	PairResets  *services.TrafficPairResetService
	Counters    *services.TrafficCountersService
	Online      *services.OnlineService
	Permissions settings.GRPCPermissions
}

func registerGRPC(p grpcServices) {
	xrayv1.RegisterXrayServiceServer(p.Registrar, grpcapi.Handler{
		Info: p.Info, Runtime: p.Runtime, Traffic: p.Traffic, Resets: p.Resets, ScopeResets: p.ScopeResets,
		Pairs: p.Pairs, PairResets: p.PairResets, TrafficCounters: p.Counters, Online: p.Online,
		AllowInfoRead: p.Permissions.ReadEngineInfo, AllowStatsRead: p.Permissions.ReadEngineStats,
		AllowOnlineRead: p.Permissions.ReadEngineOnline, AllowStatsReset: p.Permissions.ResetEngineStats,
	})
}

type capabilityResult struct {
	fx.Out
	Values []system.Capability `group:"node_capabilities,flatten"`
}

func capability() capabilityResult {
	return capabilityResult{Values: []system.Capability{
		domain.ReadBinaryInfo, domain.ReadRuntimeInfo,
		domain.ReadTrafficCounter, domain.ListTrafficCounters, domain.ResetTrafficCounter, domain.ResetTrafficCounters,
		domain.ReadTrafficPair, domain.ResetTrafficPair,
		domain.ReadUserOnlineStats, domain.ListUserOnlineIPs, domain.ListOnlineUsers,
	}}
}

type apiCleanupResult struct {
	fx.Out
	Cleanup io.Closer `group:"module_resources"`
}

func apiCleanup(resources *apiResources) apiCleanupResult {
	return apiCleanupResult{Cleanup: resources}
}
