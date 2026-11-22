// Package credentials composes bounded native Xray credential tools.
package credentials

import (
	"context"
	"io"
	"path/filepath"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/tools/credentials/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/credentials/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/credentials/grpcapi"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/credentials/httpapi"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/credentials/infra/process"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/credentials/natsapi"
	"github.com/PapilioVPN/node/internal/platform/natsclient"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/gofiber/fiber/v3"
	"go.uber.org/fx"
	"google.golang.org/grpc"
)

func Module(config []settings.Instance, requests settings.Requests) fx.Option {
	return fx.Module("xray-credentials", fx.Supply(config, requests, fx.Private), fx.Provide(compose, fx.Private), fx.Invoke(register, lifecycle), fx.Provide(subscribe), fx.Provide(fx.Annotate(finalize, fx.ResultTags(`group:"final-resources"`))))
}
func compose(config []settings.Instance) (*services.Service, *process.Executor, error) {
	executor := process.NewExecutor()
	bindings := make([]services.Binding, 0, len(config))
	for _, instance := range config {
		binding := services.Binding{ID: instance.ID}
		if instance.Xray != nil {
			runner, err := process.New(process.Config{Binary: instance.Xray.Binary, Directory: filepath.Dir(instance.Xray.Configuration), WorkDirectory: instance.Xray.WorkDirectory}, executor)
			if err != nil {
				return nil, nil, err
			}
			binding.Generator = runner
		}
		bindings = append(bindings, binding)
	}
	service, err := services.New(bindings)
	return service, executor, err
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
		{Name: "Xray credential UUID", SubjectSuffix: "xray.tools.credentials.uuid.v1.generate", Handle: h.UUID},
		{Name: "Xray credential X25519", SubjectSuffix: "xray.tools.credentials.x25519.v1.generate", Handle: h.X25519},
		{Name: "Xray credential WireGuard", SubjectSuffix: "xray.tools.credentials.wireguard.v1.generate", Handle: h.WireGuard},
		{Name: "Xray credential MLDSA65", SubjectSuffix: "xray.tools.credentials.mldsa65.v1.generate", Handle: h.MLDSA65},
		{Name: "Xray credential MLKEM768", SubjectSuffix: "xray.tools.credentials.mlkem768.v1.generate", Handle: h.MLKEM768},
		{Name: "Xray credential VLESSEncryption", SubjectSuffix: "xray.tools.credentials.vless.v1.generate", Handle: h.VLESSEncryption},
		{Name: "Xray credential Certificate", SubjectSuffix: "xray.tools.credentials.tls.certificates.v1.generate", Handle: h.Certificate},
		{Name: "Xray credential CertificateHashes", SubjectSuffix: "xray.tools.credentials.tls.hashes.v1.get", Handle: h.CertificateHashes},
		{Name: "Xray credential ECH", SubjectSuffix: "xray.tools.credentials.ech.v1.generate", Handle: h.ECH},
	}}
}
func lifecycle(lc fx.Lifecycle, owner *process.Executor) { lc.Append(fx.Hook{OnStop: owner.Close}) }

type finalizer struct{ owner *process.Executor }

func finalize(owner *process.Executor) io.Closer { return finalizer{owner} }
func (f finalizer) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), domain.ExecutionTimeout+time.Second)
	defer cancel()
	return f.owner.Close(ctx)
}
