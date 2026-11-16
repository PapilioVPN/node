// Package info composes observations of Xray's configured binary and native runtime.
package info

import (
	"errors"
	"fmt"

	"go.uber.org/fx"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	system "github.com/PapilioVPN/node/internal/modules/system/info/domain"
	"github.com/PapilioVPN/node/internal/modules/xray/info/app/services"
	"github.com/PapilioVPN/node/internal/modules/xray/info/domain"
	"github.com/PapilioVPN/node/internal/modules/xray/info/grpcapi"
	"github.com/PapilioVPN/node/internal/modules/xray/info/infra/process"
	"github.com/PapilioVPN/node/internal/settings"
)

type BackendBinding struct {
	InstanceID instances.InstanceID
	Reader     services.RuntimeInfoReader
}

type backendBindings struct {
	fx.In
	Values []BackendBinding `group:"xray_info_backends"`
}

func Module(grpcEnabled bool) fx.Option {
	options := []fx.Option{fx.Provide(newBinaryInfoService, newRuntimeInfoService, fx.Private), fx.Provide(capability)}
	if grpcEnabled {
		options = append(options, fx.Provide(newInfoAPI))
	}
	return fx.Module("xray-info", options...)
}

func newBinaryInfoService(config []settings.Instance) (*services.BinaryInfoService, error) {
	readers := make(map[instances.InstanceID]services.BinaryInfoReader)
	for _, instance := range config {
		if instance.Engine != instances.Xray {
			continue
		}
		readers[instance.ID] = nil
		if inspection := instance.Xray.Inspection; inspection != nil {
			reader, err := process.NewBinaryInspector(process.BinaryInspectionConfig{
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

func newRuntimeInfoService(bindings backendBindings) (*services.RuntimeInfoService, error) {
	readers := make(map[instances.InstanceID]services.RuntimeInfoReader, len(bindings.Values))
	for _, b := range bindings.Values {
		if _, duplicate := readers[b.InstanceID]; duplicate || b.InstanceID == "" {
			return nil, errors.New("invalid Xray info binding")
		}
		readers[b.InstanceID] = b.Reader
	}
	return services.NewRuntimeInfoService(readers), nil
}

func newInfoAPI(binary *services.BinaryInfoService, runtime *services.RuntimeInfoService, p settings.GRPCPermissions) grpcapi.InfoAPI {
	return grpcapi.InfoAPI{Info: binary, Runtime: runtime, AllowInfoRead: p.ReadEngineInfo}
}

type capabilityResult struct {
	fx.Out
	Values []system.Capability `group:"node_capabilities,flatten"`
}

func capability() capabilityResult {
	return capabilityResult{Values: []system.Capability{domain.ReadBinaryInfo, domain.ReadRuntimeInfo}}
}
