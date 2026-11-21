// Package instances composes configured runtime owners and their management APIs.
package instances

import (
	"context"
	"errors"
	"io"
	"math"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/grpcapi"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/httpapi"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/process"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/natsapi"
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
		fx.Provide(fx.Annotate(finalize, fx.ResultTags(`group:"final-resources"`))),
		fx.Invoke(registerLifecycle, register), fx.Provide(subscribe),
	)
}
func compose(config []settings.Instance) (*services.Service, error) {
	bindings := make([]services.Binding, 0, len(config))
	for _, instance := range config {
		if instance.Engine != "xray" || instance.Xray == nil {
			return nil, errors.New("unsupported instance engine")
		}
		var api *process.APIConfig
		if instance.Xray.API != nil {
			api = &process.APIConfig{Network: instance.Xray.API.Network, Address: instance.Xray.API.Address}
		}
		runtime, err := process.New(process.Config{Binary: instance.Xray.Binary, Configuration: instance.Xray.Configuration, StopTimeout: instance.StopTimeout, API: api})
		if err != nil {
			return nil, err
		}
		bindings = append(bindings, services.Binding{ID: instance.ID, Engine: instance.Engine, Runtime: runtime})
	}
	return services.New(bindings)
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
