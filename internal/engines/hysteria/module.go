// Package hysteria composes the standalone Hysteria 2 server capabilities.
package hysteria

import (
	"context"

	"github.com/PapilioVPN/node/internal/engines/hysteria/configs"
	"github.com/PapilioVPN/node/internal/engines/hysteria/instances"
	"github.com/PapilioVPN/node/internal/engines/hysteria/logs"
	"github.com/PapilioVPN/node/internal/engines/hysteria/stats"
	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"
	"google.golang.org/grpc"
)

type InstanceConfig = instances.Config
type Config struct{ Instances []InstanceConfig }

type Engine struct {
	enabled   bool
	instances instances.Capability
	configs   configs.Capability
	stats     stats.Capability
	logs      logs.Capability
}

func New(config Config) (*Engine, error) {
	runtime, err := instances.New(config.Instances)
	if err != nil {
		return nil, err
	}
	files := make(map[string]string, len(config.Instances))
	for _, c := range config.Instances {
		files[c.ID] = c.ConfigFile
	}
	return &Engine{enabled: len(config.Instances) != 0, instances: runtime, configs: configs.New(files), stats: stats.New(runtime.Service), logs: logs.New(runtime.Service)}, nil
}

func (e *Engine) RegisterHTTP(router chi.Router) {
	if !e.enabled {
		return
	}
	e.instances.RegisterHTTP(router)
	e.configs.RegisterHTTP(router)
	e.stats.RegisterHTTP(router)
	e.logs.RegisterHTTP(router)
}
func (e *Engine) RegisterGRPC(registrar grpc.ServiceRegistrar) {
	if !e.enabled {
		return
	}
	e.instances.RegisterGRPC(registrar)
	e.configs.RegisterGRPC(registrar)
	e.stats.RegisterGRPC(registrar)
	e.logs.RegisterGRPC(registrar)
}

// CloseLogs releases streaming RPCs before the management listeners drain.
func (e *Engine) CloseLogs() { e.logs.Close() }

func (e *Engine) Close(ctx context.Context) error {
	e.CloseLogs()
	return e.instances.Close(ctx)
}

var Module = fx.Module("hysteria", fx.Provide(New), fx.Invoke(register))

func register(lc fx.Lifecycle, engine *Engine, router *chi.Mux, server *grpc.Server) {
	if !engine.enabled {
		return
	}
	engine.RegisterHTTP(router)
	engine.RegisterGRPC(server)
	lc.Append(fx.Hook{OnStop: engine.Close})
}
