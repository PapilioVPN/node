// Package outbounds composes Xray outbound observation operations.
package outbounds

import (
	"errors"
	"fmt"

	"go.uber.org/fx"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	system "github.com/PapilioVPN/node/internal/modules/system/info/domain"
	"github.com/PapilioVPN/node/internal/modules/xray/outbounds/app/services"
	"github.com/PapilioVPN/node/internal/modules/xray/outbounds/domain"
	"github.com/PapilioVPN/node/internal/modules/xray/outbounds/grpcapi"
	outboundapi "github.com/PapilioVPN/node/internal/modules/xray/outbounds/infra/api"
	nativeapi "github.com/PapilioVPN/node/internal/platform/xray"
	"github.com/PapilioVPN/node/internal/settings"
)

type BackendBinding struct {
	InstanceID instances.InstanceID
	Reader     services.OutboundStatusReader
}

type backendBindings struct {
	fx.In
	Values []BackendBinding `group:"xray_outbound_backends"`
}

func Module(grpcEnabled bool) fx.Option {
	options := []fx.Option{fx.Provide(newNativeBindings, capability), fx.Provide(newService, fx.Private)}
	if grpcEnabled {
		options = append(options, fx.Provide(newAPI))
	}
	return fx.Module("xray-outbounds", options...)
}

func newService(bindings backendBindings) (*services.OutboundStatusService, error) {
	readers := make(map[instances.InstanceID]services.OutboundStatusReader, len(bindings.Values))
	for _, b := range bindings.Values {
		if _, duplicate := readers[b.InstanceID]; duplicate || b.InstanceID == "" {
			return nil, errors.New("invalid Xray outbound binding")
		}
		readers[b.InstanceID] = b.Reader
	}
	return services.NewOutboundStatusService(readers), nil
}

func newAPI(service *services.OutboundStatusService, p settings.GRPCPermissions) grpcapi.OutboundAPI {
	return grpcapi.OutboundAPI{Outbounds: service, AllowOutboundsRead: p.ReadEngineOutbounds}
}

type capabilityResult struct {
	fx.Out
	Values []system.Capability `group:"node_capabilities,flatten"`
}

func capability() capabilityResult {
	return capabilityResult{Values: []system.Capability{domain.ReadOutboundStatus, domain.ListOutboundStatuses}}
}

type nativeBindings struct {
	fx.Out
	Values []BackendBinding `group:"xray_outbound_backends,flatten"`
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
			binding.Reader = &outboundapi.Observer{API: client}
		}
		result.Values = append(result.Values, binding)
	}
	return result, nil
}
