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
	system "github.com/PapilioVPN/node/internal/modules/system/info/domain"
	"github.com/PapilioVPN/node/internal/modules/xray/app/services"
	"github.com/PapilioVPN/node/internal/modules/xray/domain"
	"github.com/PapilioVPN/node/internal/modules/xray/grpcapi"
	xrayv1 "github.com/PapilioVPN/node/internal/modules/xray/grpcapi/proto/v1"
	xrayapi "github.com/PapilioVPN/node/internal/modules/xray/infra/api"
	xrayprocess "github.com/PapilioVPN/node/internal/modules/xray/infra/process"
	routingowner "github.com/PapilioVPN/node/internal/modules/xray/routing"
	routingservices "github.com/PapilioVPN/node/internal/modules/xray/routing/app/services"
	routingdomain "github.com/PapilioVPN/node/internal/modules/xray/routing/domain"
	routinggrpc "github.com/PapilioVPN/node/internal/modules/xray/routing/grpcapi"
	routingxray "github.com/PapilioVPN/node/internal/modules/xray/routing/infra/api"
	routingprocess "github.com/PapilioVPN/node/internal/modules/xray/routing/infra/process"
	trafficowner "github.com/PapilioVPN/node/internal/modules/xray/traffic"
	trafficdomain "github.com/PapilioVPN/node/internal/modules/xray/traffic/domain"
	trafficgrpc "github.com/PapilioVPN/node/internal/modules/xray/traffic/grpcapi"
	trafficxray "github.com/PapilioVPN/node/internal/modules/xray/traffic/infra/api"
	userowner "github.com/PapilioVPN/node/internal/modules/xray/users"
	userdomain "github.com/PapilioVPN/node/internal/modules/xray/users/domain"
	usergrpc "github.com/PapilioVPN/node/internal/modules/xray/users/grpcapi"
	userxray "github.com/PapilioVPN/node/internal/modules/xray/users/infra/api"
	nativeapi "github.com/PapilioVPN/node/internal/platform/engines/xray/api"
	"github.com/PapilioVPN/node/internal/settings"
)

// Module composes Xray capabilities and their shared native resource bindings.
func Module(grpcEnabled bool) fx.Option {
	options := []fx.Option{
		userowner.Module(grpcEnabled), routingowner.Module(grpcEnabled), trafficowner.Module(grpcEnabled),
		fx.Provide(
			newBinaryInfoService, newAPIResources, newRuntimeInfoService,
			newOnlineService,
			newOutboundStatusService,
			fx.Private,
		),
		fx.Provide(capability, apiCleanup, newRuntimeBindings, newUserBindings, newRoutingBindings, newTrafficBindings), fx.Invoke(registerAPILifecycle),
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
	runtimeReaders  map[instances.InstanceID]services.RuntimeInfoReader
	onlineReaders   map[instances.InstanceID]services.OnlineReader
	outboundReaders map[instances.InstanceID]services.OutboundStatusReader
	clients         []*nativeapi.Client
	userBindings    []userowner.BackendBinding
	routingBindings []routingowner.BackendBinding
	trafficBindings []trafficowner.BackendBinding
}

func newAPIResources(config []settings.Instance) (*apiResources, error) {
	resources := &apiResources{
		runtimeReaders:  make(map[instances.InstanceID]services.RuntimeInfoReader),
		onlineReaders:   make(map[instances.InstanceID]services.OnlineReader),
		outboundReaders: make(map[instances.InstanceID]services.OutboundStatusReader),
	}
	for _, instance := range config {
		if instance.Engine != instances.Xray {
			continue
		}
		resources.runtimeReaders[instance.ID] = nil
		resources.onlineReaders[instance.ID] = nil
		resources.outboundReaders[instance.ID] = nil
		binding := userowner.BackendBinding{InstanceID: instance.ID}
		routing := routingowner.BackendBinding{InstanceID: instance.ID}
		traffic := trafficowner.BackendBinding{InstanceID: instance.ID}
		if api := instance.Xray.API; api != nil {
			client, err := nativeapi.NewClient(nativeapi.Config{
				Network: api.Network, Address: api.Address, RequestTimeout: api.RequestTimeout,
				MaxReceiveBytes: api.MaxReceiveBytes, MaxConcurrent: api.MaxConcurrent,
			})
			if err != nil {
				return nil, errors.Join(fmt.Errorf("construct instance %q API: %w", instance.ID, err), resources.Close())
			}
			adapter := &xrayapi.Adapter{API: client}
			resources.runtimeReaders[instance.ID] = adapter
			resources.onlineReaders[instance.ID] = adapter
			resources.outboundReaders[instance.ID] = adapter
			counters := &trafficxray.Counters{API: client}
			traffic.Reader, traffic.Collection, traffic.Resetter = counters, counters, counters
			traffic.Timeout = api.RequestTimeout
			traffic.ResetConcurrency = 1
			if profile := instance.Traffic; profile != nil {
				traffic.ResetConcurrency = profile.ResetConcurrency
			}
			router := &routingxray.Router{API: client}
			routing.Reader, routing.Prober, routing.Overrider, routing.Remover = router, router, router, router
			routing.Addition = routingservices.RoutingRuleBinding{Applier: router, Timeout: api.RequestTimeout}
			userAdapter := &userxray.Adapter{API: client}
			binding.Adder, binding.Remover, binding.Counter, binding.Observer = userAdapter, userAdapter, userAdapter, userAdapter
			resources.clients = append(resources.clients, client)
		}
		resources.userBindings = append(resources.userBindings, binding)
		resources.routingBindings = append(resources.routingBindings, routing)
		resources.trafficBindings = append(resources.trafficBindings, traffic)
	}
	return resources, nil
}

func newRuntimeInfoService(resources *apiResources) *services.RuntimeInfoService {
	return services.NewRuntimeInfoService(resources.runtimeReaders)
}

func newOnlineService(resources *apiResources) *services.OnlineService {
	return services.NewOnlineService(resources.onlineReaders)
}

func newOutboundStatusService(resources *apiResources) *services.OutboundStatusService {
	return services.NewOutboundStatusService(resources.outboundReaders)
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
	Online      *services.OnlineService
	Outbounds   *services.OutboundStatusService
	UserAPI     usergrpc.UserAPI
	RoutingAPI  routinggrpc.RoutingAPI
	TrafficAPI  trafficgrpc.TrafficAPI
	Permissions settings.GRPCPermissions
}

func registerGRPC(p grpcServices) {
	xrayv1.RegisterXrayServiceServer(p.Registrar, grpcapi.Handler{
		Info: p.Info, Runtime: p.Runtime, Online: p.Online, Outbounds: p.Outbounds,
		UserAPI: p.UserAPI, RoutingAPI: p.RoutingAPI, TrafficAPI: p.TrafficAPI,
		AllowInfoRead:      p.Permissions.ReadEngineInfo,
		AllowOnlineRead:    p.Permissions.ReadEngineOnline,
		AllowOutboundsRead: p.Permissions.ReadEngineOutbounds,
	})
}

type capabilityResult struct {
	fx.Out
	Values []system.Capability `group:"node_capabilities,flatten"`
}

func capability() capabilityResult {
	return capabilityResult{Values: []system.Capability{
		domain.ReadBinaryInfo, domain.ReadRuntimeInfo,
		trafficdomain.ReadTrafficCounter, trafficdomain.ListTrafficCounters, trafficdomain.ResetTrafficCounter, trafficdomain.ResetTrafficCounters,
		trafficdomain.ReadTrafficPair, trafficdomain.ResetTrafficPair,
		userdomain.AddInboundUser, userdomain.RemoveInboundUser, userdomain.ReadInboundUserCount,
		userdomain.ReadInboundUser, userdomain.ListInboundUsers,
		userdomain.ReadInboundUserMetadata, userdomain.ListInboundUserMetadata,
		routingdomain.ListRoutingRules, routingdomain.ReadBalancerInfo,
		routingdomain.TestRoute, routingdomain.OverrideBalancerTarget, routingdomain.RemoveRoutingRule, routingdomain.AddRoutingRule,
		domain.ReadUserOnlineStats, domain.ListUserOnlineIPs, domain.ListOnlineUsers,
		domain.ReadOutboundStatus, domain.ListOutboundStatuses,
	}}
}

type apiCleanupResult struct {
	fx.Out
	Cleanup io.Closer `group:"module_resources"`
}

func apiCleanup(resources *apiResources) apiCleanupResult {
	return apiCleanupResult{Cleanup: resources}
}

type userBindingsResult struct {
	fx.Out
	Values []userowner.BackendBinding `group:"user_backends,flatten"`
}

func newUserBindings(resources *apiResources) userBindingsResult {
	return userBindingsResult{Values: resources.userBindings}
}

type routingBindingsResult struct {
	fx.Out
	Values []routingowner.BackendBinding `group:"routing_backends,flatten"`
}

func newRoutingBindings(config []settings.Instance, resources *apiResources) (routingBindingsResult, error) {
	byID := make(map[instances.InstanceID]settings.Instance, len(config))
	for _, instance := range config {
		byID[instance.ID] = instance
	}
	result := routingBindingsResult{Values: append([]routingowner.BackendBinding(nil), resources.routingBindings...)}
	for i := range result.Values {
		binding := &result.Values[i]
		instance := byID[binding.InstanceID]
		if api := instance.Xray.API; api != nil && instance.Xray.RoutingRules != nil {
			rules := instance.Xray.RoutingRules
			preparer, err := routingprocess.NewRoutingRulePreparer(routingprocess.RoutingRulePreparationConfig{
				Binary: instance.Xray.Binary, Directory: filepath.Dir(instance.Xray.ConfigFile), WorkDir: rules.WorkDir,
				Timeout: api.RequestTimeout, MaxInputBytes: rules.MaxInputBytes, MaxOutputBytes: rules.MaxOutputBytes, MaxConcurrent: rules.MaxConcurrent,
			})
			if err != nil {
				return routingBindingsResult{}, fmt.Errorf("construct instance %q routing preparer: %w", instance.ID, err)
			}
			binding.Addition.Preparer = preparer
		}
	}
	return result, nil
}

type trafficBindingsResult struct {
	fx.Out
	Values []trafficowner.BackendBinding `group:"traffic_backends,flatten"`
}

func newTrafficBindings(resources *apiResources) trafficBindingsResult {
	return trafficBindingsResult{Values: resources.trafficBindings}
}
