// Package configs composes Xray configuration services and their adapters.
package configs

import (
	"context"
	"io"
	"math"
	"path/filepath"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/grpcapi"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/httpapi"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/infra/process"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/natsapi"
	"github.com/PapilioVPN/node/internal/platform/natsclient"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/gofiber/fiber/v3"
	"go.uber.org/fx"
	"google.golang.org/grpc"
)

func Module(config []settings.Instance, requests settings.Requests) fx.Option {
	return fx.Module("xray-configs",
		fx.Supply(config, requests, fx.Private),
		fx.Provide(compose, fx.Private),
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
		}
		bindings = append(bindings, binding)
	}
	service, err := services.New(bindings)
	return service, admission, err
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
		(httpapi.Handler{Service: in.Service, Timeout: in.Requests.Timeout}).Register(in.HTTP)
	}
	if in.GRPC != nil {
		(grpcapi.Handler{Service: in.Service, Timeout: in.Requests.Timeout}).Register(in.GRPC)
	}
}

func subscribe(in adapters) natsclient.Subscriptions {
	handler := natsapi.Handler{Service: in.Service, Timeout: in.Requests.Timeout}
	return natsclient.Subscriptions{Endpoints: []natsclient.Subscription{
		{Name: "Xray configuration validation", SubjectSuffix: "xray.runtime.configs.v1.validate", Handle: handler.Validate},
		{Name: "Xray configuration compilation", SubjectSuffix: "xray.runtime.configs.v1.compile", Handle: handler.Compile},
	}}
}

func registerLifecycle(lifecycle fx.Lifecycle, resource *process.Admission) {
	lifecycle.Append(fx.Hook{OnStop: resource.Close})
}

type finalizer struct {
	resource *process.Admission
	timeout  time.Duration
}

func finalize(resource *process.Admission, requests settings.Requests) io.Closer {
	timeout := requests.Timeout
	if timeout <= time.Duration(math.MaxInt64)-time.Second {
		timeout += time.Second
	}
	return finalizer{resource, timeout}
}
func (f finalizer) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), f.timeout)
	defer cancel()
	return f.resource.Close(ctx)
}
