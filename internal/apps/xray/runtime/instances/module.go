// Package instances composes configured runtime owners and their management APIs.
package instances

import (
	"context"
	"errors"
	"io"
	"math"
	"time"

	routingservices "github.com/PapilioVPN/node/internal/apps/xray/network/routing/app/services"
	logservices "github.com/PapilioVPN/node/internal/apps/xray/observability/loggers/app/services"
	healthservices "github.com/PapilioVPN/node/internal/apps/xray/observability/outboundhealth/app/services"
	trafficservices "github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/app/services"
	inboundservices "github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/app/services"
	outboundservices "github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/app/services"
	configservices "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/grpcapi"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/httpapi"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/process"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/rpc"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/natsapi"
	statservices "github.com/PapilioVPN/node/internal/apps/xray/runtime/statistics/app/services"
	assetservices "github.com/PapilioVPN/node/internal/apps/xray/tools/assets/app/services"
	assetinstances "github.com/PapilioVPN/node/internal/apps/xray/tools/assets/infra/instances"
	diagnosticservices "github.com/PapilioVPN/node/internal/apps/xray/tools/diagnostics/app/services"
	accountservices "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/app/services"
	onlineservices "github.com/PapilioVPN/node/internal/apps/xray/users/online/app/services"
	"github.com/PapilioVPN/node/internal/platform/natsclient"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/gofiber/fiber/v3"
	"go.uber.org/fx"
	"google.golang.org/grpc"
)

func Module(config []settings.Instance, requests settings.Requests) fx.Option {
	return fx.Module("instances",
		fx.Supply(config, requests, fx.Private),
		fx.Provide(compose, fx.Private),
		fx.Provide(fx.Annotate(func(bindings apiBindings) []rpc.Binding { return []rpc.Binding(bindings) }, fx.ResultTags(`group:"xray-native-api,flatten"`))),
		fx.Provide(fx.Annotate(func(bindings outputBindings) []domain.OutputBinding { return []domain.OutputBinding(bindings) }, fx.ResultTags(`group:"xray-instance-output,flatten"`))),
		fx.Provide(func(service *services.Service) configservices.Restarter { return service }),
		fx.Provide(func(service *services.Service) statservices.StatusReader { return service }),
		fx.Provide(func(service *services.Service) logservices.StatusReader { return service }),
		fx.Provide(func(service *services.Service) inboundservices.StatusReader { return service }),
		fx.Provide(func(service *services.Service) outboundservices.StatusReader { return service }),
		fx.Provide(func(service *services.Service) accountservices.StatusReader { return service }),
		fx.Provide(func(service *services.Service) onlineservices.StatusReader { return service }),
		fx.Provide(func(service *services.Service) trafficservices.StatusReader { return service }),
		fx.Provide(func(service *services.Service) routingservices.StatusReader { return service }),
		fx.Provide(func(service *services.Service) healthservices.StatusReader { return service }),
		fx.Provide(func(service *services.Service) assetservices.Lifecycle {
			return assetinstances.Lifecycle{Acquire: service.ReserveExecution}
		}),
		fx.Provide(func(service *services.Service) diagnosticservices.StatusReader { return service }),
		fx.Provide(fx.Annotate(finalize, fx.ResultTags(`group:"final-resources"`))),
		fx.Invoke(register), fx.Provide(subscribe),
	)
}

type composition struct {
	fx.In
	Config    []settings.Instance
	Lifecycle fx.Lifecycle
	Sources   process.SourceFactory `optional:"true"`
}

type apiBindings []rpc.Binding
type outputBindings []domain.OutputBinding

func compose(in composition) (*services.Service, apiBindings, outputBindings, error) {
	var apis apiBindings
	var outputs outputBindings
	config := in.Config
	bindings := make([]services.Binding, 0, len(config))
	for _, instance := range config {
		if instance.Engine != "xray" || instance.Xray == nil {
			return nil, nil, nil, errors.New("unsupported instance engine")
		}
		var api *rpc.Client
		if instance.Xray.API != nil {
			var err error
			api, err = rpc.New(rpc.Config{Network: instance.Xray.API.Network, Address: instance.Xray.API.Address})
			if err != nil {
				return nil, nil, nil, err
			}
		}
		var source process.Source
		if in.Sources != nil {
			source = in.Sources.ForInstance(instance.ID)
		}
		runtime, err := process.New(process.Config{ID: instance.ID, RecoveryDirectory: instance.Xray.StateDirectory, WorkDirectory: instance.Xray.WorkDirectory, Source: source, Binary: instance.Xray.Binary, Configuration: instance.Xray.Configuration, StopTimeout: instance.StopTimeout, API: api})
		if err != nil {
			return nil, nil, nil, err
		}
		apis = append(apis, rpc.Binding{ID: instance.ID, Client: api})
		outputs = append(outputs, domain.OutputBinding{ID: instance.ID, Source: runtime})
		bindings = append(bindings, services.Binding{ID: instance.ID, Engine: instance.Engine, Runtime: runtime, Retainer: runtime, Leases: runtime})
	}
	service, err := services.New(bindings)
	if err == nil {
		registerLifecycle(in.Lifecycle, service, config)
	}
	return service, apis, outputs, err
}
func registerLifecycle(lifecycle fx.Lifecycle, service *services.Service, config []settings.Instance) {
	// This hook is installed before communication hooks. Shutdown drains incoming
	// operations before joining engines. Fx skips OnStop for a failing OnStart;
	// that failure path therefore owns its own independent rollback budget.
	budget := cleanupBudget(config)
	lifecycle.Append(fx.Hook{OnStart: func(ctx context.Context) error {
		if err := service.StartAll(ctx); err != nil {
			rollback, cancel := context.WithTimeout(context.Background(), budget)
			defer cancel()
			return errors.Join(err, service.Close(rollback))
		}
		if err := service.StartLeases(ctx); err != nil {
			rollback, cancel := context.WithTimeout(context.Background(), budget)
			defer cancel()
			return errors.Join(err, service.Close(rollback))
		}
		return nil
	}, OnStop: service.Close})
}

type adapters struct {
	fx.In
	Service  *services.Service
	Requests settings.Requests
	HTTP     *fiber.App   `optional:"true"`
	GRPC     *grpc.Server `optional:"true"`
}

func register(in adapters) {
	if in.HTTP != nil {
		httpapi.Handler{Service: in.Service, Timeout: in.Requests.Timeout}.Register(in.HTTP)
	}
	if in.GRPC != nil {
		grpcapi.Handler{Service: in.Service, Timeout: in.Requests.Timeout}.Register(in.GRPC)
	}
}
func subscribe(in adapters) natsclient.Subscriptions {
	h := natsapi.Handler{Service: in.Service, Timeout: in.Requests.Timeout}
	return natsclient.Subscriptions{Endpoints: []natsclient.Subscription{
		{Name: "instance inventory", SubjectSuffix: "core.engines.instances.list.v1.get", Handle: h.List},
		{Name: "instance repair", SubjectSuffix: "core.engines.instances.v1.repair", Handle: h.Repair},
		{Name: "instance retention", SubjectSuffix: "core.engines.instances.retention.v1.set", Handle: h.Retention},
		{Name: "instance lease", SubjectSuffix: "core.engines.instances.lease.v1.get", Handle: h.Lease},
		{Name: "instance lease acquisition", SubjectSuffix: "core.engines.instances.lease.v1.acquire", Handle: h.AcquireLease},
		{Name: "instance lease renewal", SubjectSuffix: "core.engines.instances.lease.v1.renew", Handle: h.RenewLease},
		{Name: "instance lease release", SubjectSuffix: "core.engines.instances.lease.v1.release", Handle: h.ReleaseLease},
		{Name: "instance status", SubjectSuffix: "core.engines.instances.status.v1.get", Handle: h.Status},
		{Name: "instance start", SubjectSuffix: "core.engines.instances.v1.start", Handle: h.Start},
		{Name: "instance stop", SubjectSuffix: "core.engines.instances.v1.stop", Handle: h.Stop},
		{Name: "instance restart", SubjectSuffix: "core.engines.instances.v1.restart", Handle: h.Restart},
		{Name: "instance readiness", SubjectSuffix: "core.engines.instances.readiness.v1.get", Handle: h.Check},
	}}
}

// finalizer is standard io.Closer composition for the actual child executions,
// not a second lifecycle framework. Normal and final cleanup are idempotent.
type finalizer struct {
	service *services.Service
	budget  time.Duration
}

func finalize(service *services.Service, config []settings.Instance) io.Closer {
	return finalizer{service, cleanupBudget(config)}
}
func (f finalizer) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), f.budget)
	defer cancel()
	return f.service.Close(ctx)
}
func cleanupBudget(config []settings.Instance) time.Duration {
	var budget time.Duration
	for _, instance := range config {
		if instance.StopTimeout > time.Duration(math.MaxInt64)-budget {
			return time.Duration(math.MaxInt64)
		}
		budget += instance.StopTimeout
	}
	if budget > time.Duration(math.MaxInt64)-time.Second {
		return time.Duration(math.MaxInt64)
	}
	return budget + time.Second
}
