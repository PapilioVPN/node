// Package assets composes explicit Xray executable/geodata preparation and recovery.
package assets

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/tools/assets/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/assets/grpcapi"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/assets/httpapi"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/assets/infra/store"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/assets/natsapi"
	"github.com/PapilioVPN/node/internal/platform/natsclient"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/gofiber/fiber/v3"
	"go.uber.org/fx"
	"google.golang.org/grpc"
)

// Config is supplied by the settings/integration owner. Directory is an existing
// dedicated 0700 root, separate from private config/state/work directories.
// Instances must select Directory/current/xray for every managed execution.
type Config struct{ ID, Directory string }

func Module(config []Config, requests settings.Requests) fx.Option {
	return fx.Module("xray-assets", fx.Supply(config, requests, fx.Private), fx.Provide(compose, fx.Private), fx.Invoke(register, lifecycle), fx.Provide(subscribe), fx.Provide(fx.Annotate(finalize, fx.ResultTags(`group:"final-resources"`))))
}

type inputs struct {
	fx.In
	Config    []Config
	Lifecycle services.Lifecycle `optional:"true"`
}
type resources struct {
	service *services.Service
	stores  []*store.Store
	once    sync.Once
	err     error
}

func compose(in inputs) (*services.Service, *resources, error) {
	owner := &resources{}
	bindings := make([]services.Binding, 0, len(in.Config))
	for _, c := range in.Config {
		r, err := store.Open(c.Directory, c.ID)
		if err != nil {
			for _, r := range owner.stores {
				_ = r.Close()
			}
			return nil, nil, err
		}
		owner.stores = append(owner.stores, r)
		bindings = append(bindings, services.Binding{ID: c.ID, Repository: r})
	}
	service, err := services.New(bindings, in.Lifecycle)
	if err != nil {
		for _, r := range owner.stores {
			_ = r.Close()
		}
		return nil, nil, err
	}
	owner.service = service
	return service, owner, nil
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
		{Name: "Xray asset inspection", SubjectSuffix: "xray.tools.assets.v1.inspect", Handle: h.Inspect},
		{Name: "Xray asset staging", SubjectSuffix: "xray.tools.assets.v1.stage", Handle: h.Stage},
		{Name: "Xray asset activation", SubjectSuffix: "xray.tools.assets.v1.activate", Handle: h.Activate},
		{Name: "Xray asset rollback", SubjectSuffix: "xray.tools.assets.v1.rollback", Handle: h.Rollback},
		{Name: "Xray asset recovery", SubjectSuffix: "xray.tools.assets.v1.recover", Handle: h.Recover},
	}}
}
func lifecycle(lc fx.Lifecycle, owner *resources) { lc.Append(fx.Hook{OnStop: owner.service.Close}) }
func finalize(owner *resources) io.Closer         { return owner }
func (r *resources) Close() error {
	// Recovery can outlive the request deadline by at most its explicit 30s budget.
	// Successful join is required before stores close. A failed join is retryable.
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	if err := r.service.Close(ctx); err != nil {
		return err
	}
	r.once.Do(func() {
		for i := len(r.stores) - 1; i >= 0; i-- {
			r.err = errors.Join(r.err, r.stores[i].Close())
		}
	})
	return r.err
}
