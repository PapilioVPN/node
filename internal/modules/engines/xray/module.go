// Package xray composes Xray-specific operations; instances owns runtime control.
package xray

import (
	"fmt"

	"go.uber.org/fx"
	"google.golang.org/grpc"

	"github.com/PapilioVPN/node/internal/modules/engines/xray/app"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/grpcapi"
	xrayv1 "github.com/PapilioVPN/node/internal/modules/engines/xray/grpcapi/proto/v1"
	instances "github.com/PapilioVPN/node/internal/modules/instances/domain"
	system "github.com/PapilioVPN/node/internal/modules/system/domain"
	native "github.com/PapilioVPN/node/internal/platform/xray"
	"github.com/PapilioVPN/node/internal/settings"
)

// Module owns engine information without constructing another runtime or hook.
func Module(grpcEnabled bool) fx.Option {
	options := []fx.Option{fx.Provide(newBinaryInfoService, fx.Private), fx.Provide(capability)}
	if grpcEnabled {
		options = append(options, fx.Invoke(registerGRPC))
	}
	return fx.Module("xray", options...)
}

func newBinaryInfoService(config []settings.Instance) (*app.BinaryInfoService, error) {
	readers := make(map[instances.InstanceID]app.BinaryInfoReader)
	for _, instance := range config {
		if instance.Engine != instances.Xray {
			continue
		}
		readers[instance.ID] = nil
		if inspection := instance.Xray.Inspection; inspection != nil {
			reader, err := native.NewBinaryInspector(native.BinaryInspectionConfig{
				Binary: instance.Xray.Binary, Timeout: inspection.Timeout,
				MaxOutputBytes: inspection.MaxOutputBytes, MaxConcurrent: inspection.MaxConcurrent,
			})
			if err != nil {
				return nil, fmt.Errorf("construct instance %q binary inspector: %w", instance.ID, err)
			}
			readers[instance.ID] = reader
		}
	}
	return app.NewBinaryInfoService(readers), nil
}

func registerGRPC(registrar grpc.ServiceRegistrar, info *app.BinaryInfoService, cfg settings.GRPCPermissions) {
	xrayv1.RegisterXrayServiceServer(registrar, grpcapi.Handler{Info: info, AllowInfoRead: cfg.ReadEngineInfo})
}

type capabilityResult struct {
	fx.Out
	Values []system.Capability `group:"node_capabilities,flatten"`
}

func capability() capabilityResult {
	return capabilityResult{Values: []system.Capability{domain.ReadBinaryInfo}}
}
