// Package info composes observations of Xray's configured binary and native runtime.
package info

import (
	"errors"
	"fmt"

	"go.uber.org/fx"

	instances "github.com/PapilioVPN/node/internal/apps/core/engines/instances/domain"
	system "github.com/PapilioVPN/node/internal/apps/system/node/info/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/info/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/info/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/info/grpcapi"
	infoapi "github.com/PapilioVPN/node/internal/apps/xray/runtime/info/infra/api"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/info/infra/process"
	nativeapi "github.com/PapilioVPN/node/internal/platform/xray"
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
	options := []fx.Option{fx.Provide(newNativeBindings), fx.Provide(newBinaryInfoService, newRuntimeInfoService, fx.Private), fx.Provide(capability)}
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

type nativeBindings struct {
	fx.Out
	Values []BackendBinding `group:"xray_info_backends,flatten"`
}

func newNativeBindings(config []settings.Instance, resources *nativeapi.Resources) (nativeBindings, error) {
	result := nativeBindings{}
	for _, instance := range config {
		if instance.Engine != instances.Xray {
			continue
		}
		client, exists := resources.Client(string(instance.ID))
		if !exists {
			return nativeBindings{}, fmt.Errorf("missing Xray API binding for instance %q", instance.ID)
		}
		binding := BackendBinding{InstanceID: instance.ID}
		if client != nil {
			binding.Reader = &infoapi.Reader{API: client}
		}
		result.Values = append(result.Values, binding)
	}
	return result, nil
}
