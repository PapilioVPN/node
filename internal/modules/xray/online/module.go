// Package online composes native user presence and IP observations.
package online

import (
	"errors"
	"fmt"

	"go.uber.org/fx"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	system "github.com/PapilioVPN/node/internal/modules/system/info/domain"
	"github.com/PapilioVPN/node/internal/modules/xray/online/app/services"
	"github.com/PapilioVPN/node/internal/modules/xray/online/domain"
	"github.com/PapilioVPN/node/internal/modules/xray/online/grpcapi"
	onlineapi "github.com/PapilioVPN/node/internal/modules/xray/online/infra/api"
	nativeapi "github.com/PapilioVPN/node/internal/platform/xray"
	"github.com/PapilioVPN/node/internal/settings"
)

// BackendBinding contributes one instance's presence reader. A nil Reader marks
// native observations disabled; the shared resource owner retains its lifetime.
type BackendBinding struct {
	InstanceID instances.InstanceID
	Reader     services.OnlineReader
}

type backendBindings struct {
	fx.In
	Values []BackendBinding `group:"online_backends"`
}

func Module(grpcEnabled bool) fx.Option {
	options := []fx.Option{fx.Provide(newNativeBindings, capability), fx.Provide(newService, fx.Private)}
	if grpcEnabled {
		options = append(options, fx.Provide(newAPI))
	}
	return fx.Module("online", options...)
}

func newService(bindings backendBindings) (*services.OnlineService, error) {
	readers := make(map[instances.InstanceID]services.OnlineReader, len(bindings.Values))
	for _, binding := range bindings.Values {
		if _, duplicate := readers[binding.InstanceID]; duplicate || binding.InstanceID == "" {
			return nil, errors.New("invalid online backend binding")
		}
		readers[binding.InstanceID] = binding.Reader
	}
	return services.NewOnlineService(readers), nil
}

func newAPI(service *services.OnlineService, permissions settings.GRPCPermissions) grpcapi.OnlineAPI {
	return grpcapi.OnlineAPI{Online: service, AllowOnlineRead: permissions.ReadEngineOnline}
}

type capabilityResult struct {
	fx.Out
	Values []system.Capability `group:"node_capabilities,flatten"`
}

func capability() capabilityResult {
	return capabilityResult{Values: []system.Capability{domain.ReadUserOnlineStats, domain.ListUserOnlineIPs, domain.ListOnlineUsers}}
}

type nativeBindings struct {
	fx.Out
	Values []BackendBinding `group:"online_backends,flatten"`
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
			binding.Reader = &onlineapi.Online{API: client}
		}
		result.Values = append(result.Values, binding)
	}
	return result, nil
}
