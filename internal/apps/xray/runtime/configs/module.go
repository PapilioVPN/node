// Package configs composes Xray configuration services and their adapters.
package configs

import (
	"context"
	"errors"
	"io"
	"math"
	"path/filepath"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/grpcapi"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/httpapi"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/infra/process"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/infra/state"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/natsapi"
	instanceprocess "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/process"
	"github.com/PapilioVPN/node/internal/platform/natsclient"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/gofiber/fiber/v3"
	"go.uber.org/fx"
	"google.golang.org/grpc"
)

func Module(config []settings.Instance, requests settings.Requests) fx.Option {
	return fx.Module("xray-configs",
		fx.Supply(config, requests, fx.Private),
		fx.Provide(compose, composeStores, composeStateService, fx.Private),
		fx.Provide(sourceProvider),
		fx.Invoke(register, registerLifecycle),
		fx.Provide(subscribe),
		fx.Provide(fx.Annotate(finalize, fx.ResultTags(`group:"final-resources"`))),
	)
}

func compose(config []settings.Instance) (*services.Service, *process.Admission, error) {
	// Native conversion is CPU/memory work. The whole capability admits at most
	// two converter children across all instances, with no waiting work queue.
	admission, err := process.NewAdmission(2)
	if err != nil {
		return nil, nil, err
	}
	bindings := make([]services.Binding, 0, len(config))
	for _, instance := range config {
		binding := services.Binding{ID: instance.ID}
		if instance.Xray.WorkDirectory != "" {
			builder, err := process.New(process.Config{Binary: instance.Xray.Binary, Directory: filepath.Dir(instance.Xray.Configuration), WorkDirectory: instance.Xray.WorkDirectory}, admission)
			if err != nil {
				return nil, nil, err
			}
			binding.Builder = builder
			binding.Compiler = builder
			binding.SourceBuilder = builder
		}
		bindings = append(bindings, binding)
	}
	service, err := services.New(bindings)
	return service, admission, err
}

type adapters struct {
	fx.In
	Service  *services.Service
	State    *services.StateService
	Requests settings.Requests
	HTTP     *fiber.App   `optional:"true"`
	GRPC     *grpc.Server `optional:"true"`
}

func register(in adapters) {
	if in.HTTP != nil {
		(httpapi.Handler{Service: in.Service, State: in.State, Timeout: in.Requests.Timeout}).Register(in.HTTP)
	}
	if in.GRPC != nil {
		(grpcapi.Handler{Service: in.Service, State: in.State, Timeout: in.Requests.Timeout}).Register(in.GRPC)
	}
}

func subscribe(in adapters) natsclient.Subscriptions {
	handler := natsapi.Handler{Service: in.Service, State: in.State, Timeout: in.Requests.Timeout}
	return natsclient.Subscriptions{Endpoints: []natsclient.Subscription{
		{Name: "Xray configuration validation", SubjectSuffix: "xray.runtime.configs.v1.validate", Handle: handler.Validate},
		{Name: "Xray configuration compilation", SubjectSuffix: "xray.runtime.configs.v1.compile", Handle: handler.Compile},
		{Name: "Xray desired configuration", SubjectSuffix: "xray.runtime.configs.v1.get", Handle: handler.Get},
		{Name: "Xray configuration metadata", SubjectSuffix: "xray.runtime.configs.status.v1.get", Handle: handler.Metadata},
		{Name: "Xray configuration replacement", SubjectSuffix: "xray.runtime.configs.v1.replace", Handle: handler.Replace},
		{Name: "Xray configuration application", SubjectSuffix: "xray.runtime.configs.v1.apply", Handle: handler.Apply},
	}}
}

func registerLifecycle(lifecycle fx.Lifecycle, resource *process.Admission) {
	lifecycle.Append(fx.Hook{OnStop: resource.Close})
}

type finalizer struct {
	resource *process.Admission
	state    *services.StateService
	stores   *state.Collection
	timeout  time.Duration
}

func finalize(resource *process.Admission, service *services.StateService, stores *state.Collection, requests settings.Requests) io.Closer {
	timeout := requests.Timeout
	if timeout <= time.Duration(math.MaxInt64)-time.Second {
		timeout += time.Second
	}
	return finalizer{resource, service, stores, timeout}
}
func (f finalizer) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), f.timeout)
	defer cancel()
	return errors.Join(f.state.Close(ctx), f.resource.Close(ctx), f.stores.Close(ctx))
}

func composeStores(lifecycle fx.Lifecycle, config []settings.Instance) (*state.Collection, error) {
	var specs []state.Spec
	for _, instance := range config {
		if instance.Xray.StateDirectory != "" {
			specs = append(specs, state.Spec{ID: instance.ID, Directory: instance.Xray.StateDirectory})
		}
	}
	stores, err := state.NewCollection(specs)
	if err != nil {
		return nil, err
	}
	lifecycle.Append(fx.Hook{OnStart: stores.Open, OnStop: stores.Close})
	return stores, nil
}

type sourceFactory struct {
	sources map[string]instanceprocess.Source
}

func (f sourceFactory) ForInstance(id string) instanceprocess.Source { return f.sources[id] }
func sourceProvider(stores *state.Collection, config []settings.Instance) instanceprocess.SourceFactory {
	sources := make(map[string]instanceprocess.Source)
	for _, instance := range config {
		if store := stores.Store(instance.ID); store != nil {
			sources[instance.ID] = process.Resolver{Reader: store, WorkDirectory: instance.Xray.WorkDirectory}
		}
	}
	return sourceFactory{sources}
}

func composeStateService(lifecycle fx.Lifecycle, stores *state.Collection, prep *services.Service, restarter services.Restarter, config []settings.Instance) (*services.StateService, error) {
	bindings := make([]services.StateBinding, 0, len(config))
	for _, instance := range config {
		binding := services.StateBinding{ID: instance.ID}
		if store := stores.Store(instance.ID); store != nil {
			binding.Repository = store
		}
		bindings = append(bindings, binding)
	}
	service, err := services.NewState(bindings, prep, restarter)
	if err != nil {
		return nil, err
	}
	lifecycle.Append(fx.Hook{OnStop: service.Close})
	return service, nil
}
