// Package sync composes scoped Xray account reconciliation and bounded transfers.
package sync

import (
	"context"

	inboundservices "github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/app/services"
	configservices "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/app/services"
	accountservices "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/app/services"
	accountinbounds "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/inbounds"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/grpcapi"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/httpapi"
	accountadapter "github.com/PapilioVPN/node/internal/apps/xray/users/sync/infra/accounts"
	configadapter "github.com/PapilioVPN/node/internal/apps/xray/users/sync/infra/configs"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/natsapi"
	"github.com/PapilioVPN/node/internal/platform/natsclient"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/gofiber/fiber/v3"
	"go.uber.org/fx"
	"google.golang.org/grpc"
)

func Module(config []settings.Instance, requests settings.Requests) fx.Option {
	return TransportModule(requests, fx.Supply(config, fx.Private), fx.Provide(compose, services.NewTransfers, fx.Private))
}

func TransportModule(requests settings.Requests, options ...fx.Option) fx.Option {
	return fx.Module("xray-sync", fx.Options(options...), fx.Supply(requests, fx.Private), fx.Invoke(register), fx.Provide(subscriptions))
}

func compose(config []settings.Instance, accounts *accountservices.Service, catalog *inboundservices.Catalog, memberships *configservices.Memberships) (*services.Service, error) {
	ids := make([]string, 0, len(config))
	for _, instance := range config {
		ids = append(ids, instance.ID)
	}
	configs := configadapter.V1{Source: memberships}
	observations := accountadapter.Observer{
		Source: accounts, Catalog: accountinbounds.Reader{Source: catalog}, Configs: configs,
	}
	return services.New(ids, observations, accounts, configs, accountadapter.Codec{})
}

type inputs struct {
	fx.In
	Service   *services.Service
	Transfers *services.Transfers
	Requests  settings.Requests
	HTTP      *fiber.App   `optional:"true"`
	GRPC      *grpc.Server `optional:"true"`
	Lifecycle fx.Lifecycle
}

func register(in inputs) {
	if in.HTTP != nil {
		httpapi.Handler{Service: in.Service, Transfers: in.Transfers, Timeout: in.Requests.Timeout}.Register(in.HTTP)
	}
	if in.GRPC != nil {
		grpcapi.Handler{Service: in.Service, Transfers: in.Transfers, Timeout: in.Requests.Timeout}.Register(in.GRPC)
	}
	in.Lifecycle.Append(fx.Hook{OnStop: func(context.Context) error { in.Transfers.Close(); return nil }})
}
func subscriptions(in inputs) natsclient.Subscriptions {
	h := natsapi.Handler{Service: in.Service, Transfers: in.Transfers, Timeout: in.Requests.Timeout}
	return natsclient.Subscriptions{Endpoints: []natsclient.Subscription{
		{Name: "Xray sync revision", SubjectSuffix: "xray.users.sync.v1.revision", Handle: h.Revision},
		{Name: "Xray sync preview", SubjectSuffix: "xray.users.sync.v1.preview", Handle: h.Preview},
		{Name: "Xray sync application", SubjectSuffix: "xray.users.sync.v1.apply", Handle: h.Apply},
		{Name: "Xray sync status", SubjectSuffix: "xray.users.sync.v1.status", Handle: h.Status},
		{Name: "Xray sync result refresh", SubjectSuffix: "xray.users.sync.v1.refresh", Handle: h.Refresh},
		{Name: "Xray sync recovery", SubjectSuffix: "xray.users.sync.v1.recover", Handle: h.Recover},
		{Name: "Xray sync transfer", SubjectSuffix: "xray.users.sync.transfers.v1.begin", Handle: h.Begin},
		{Name: "Xray sync chunk", SubjectSuffix: "xray.users.sync.transfers.v1.append", Handle: h.Append},
		{Name: "Xray sync transfer preview", SubjectSuffix: "xray.users.sync.transfers.v1.preview", Handle: h.Finish},
		{Name: "Xray sync transfer discard", SubjectSuffix: "xray.users.sync.transfers.v1.discard", Handle: h.Discard},
	}}
}
