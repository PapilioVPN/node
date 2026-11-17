// Package accounts composes Xray account membership management and observations.
package accounts

import (
	"errors"
	"fmt"

	"go.uber.org/fx"

	instances "github.com/PapilioVPN/node/internal/apps/core/engines/instances/domain"
	system "github.com/PapilioVPN/node/internal/apps/system/node/info/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/grpcapi"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/grpcapi/mutations"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/grpcapi/observations"
	userapi "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/api"
	nativeapi "github.com/PapilioVPN/node/internal/platform/xray"
	"github.com/PapilioVPN/node/internal/settings"
)

// BackendBinding supplies focused user capabilities of one configured instance.
// Nil ports disable their operations; the resource owner retains cleanup ownership.
type BackendBinding struct {
	InstanceID instances.InstanceID
	Adder      services.InboundUserAdder
	Additions  services.UserAdditionsBinding
	Delta      services.UserDeltaBinding
	Remover    services.InboundUserRemover
	Counter    services.InboundUserCountReader
	Observer   services.UserObservationReader
}

type backendBindings struct {
	fx.In
	Values []BackendBinding `group:"user_backends"`
}

func Module(grpcEnabled bool) fx.Option {
	options := []fx.Option{fx.Provide(newNativeBindings), fx.Provide(newOperations, fx.Private), fx.Provide(capability)}
	if grpcEnabled {
		options = append(options, fx.Provide(newUserAPI))
	}
	return fx.Module("accounts", options...)
}

type operations struct {
	addition     *services.UserAdditionService
	additions    *services.UserAdditionsService
	delta        *services.UserDeltaService
	removal      *services.UserRemovalService
	counts       *services.UserCountService
	observations *services.UserObservationService
}

func newOperations(bindings backendBindings) (operations, error) {
	adders := make(map[instances.InstanceID]services.InboundUserAdder, len(bindings.Values))
	additions := make(map[instances.InstanceID]services.UserAdditionsBinding, len(bindings.Values))
	deltas := make(map[instances.InstanceID]services.UserDeltaBinding, len(bindings.Values))
	removers := make(map[instances.InstanceID]services.InboundUserRemover, len(bindings.Values))
	counters := make(map[instances.InstanceID]services.InboundUserCountReader, len(bindings.Values))
	observers := make(map[instances.InstanceID]services.UserObservationReader, len(bindings.Values))
	for _, b := range bindings.Values {
		if _, duplicate := adders[b.InstanceID]; duplicate || b.InstanceID == "" {
			return operations{}, errors.New("invalid user backend binding")
		}
		adders[b.InstanceID] = b.Adder
		additions[b.InstanceID] = b.Additions
		deltas[b.InstanceID] = b.Delta
		removers[b.InstanceID] = b.Remover
		counters[b.InstanceID] = b.Counter
		observers[b.InstanceID] = b.Observer
	}
	return operations{
		addition:     services.NewUserAdditionService(adders),
		additions:    services.NewUserAdditionsService(additions),
		delta:        services.NewUserDeltaService(deltas),
		removal:      services.NewUserRemovalService(removers),
		counts:       services.NewUserCountService(counters),
		observations: services.NewUserObservationService(observers),
	}, nil
}

func newUserAPI(s operations, p settings.GRPCPermissions) grpcapi.UserAPI {
	return grpcapi.UserAPI{
		MutationAPI: mutations.MutationAPI{
			UserAddition:        s.addition,
			UserAdditions:       s.additions,
			UserDelta:           s.delta,
			UserRemoval:         s.removal,
			AllowUserManagement: p.ManageEngineUsers,
		},
		ObservationAPI: observations.ObservationAPI{
			UserCounts:            s.counts,
			Users:                 s.observations,
			AllowUsersRead:        p.ReadEngineUsers,
			AllowUserMetadataRead: p.ReadEngineUserMetadata,
			AllowUserAccountsRead: p.ReadEngineUserAccounts,
		},
	}
}

type capabilityResult struct {
	fx.Out
	Values []system.Capability `group:"node_capabilities,flatten"`
}

func capability() capabilityResult {
	return capabilityResult{Values: []system.Capability{
		domain.AddInboundUser, domain.AddInboundUsers, domain.ApplyInboundUserDelta, domain.RemoveInboundUser, domain.ReadInboundUserCount,
		domain.ReadInboundUser, domain.ListInboundUsers,
		domain.ReadInboundUserMetadata, domain.ListInboundUserMetadata,
	}}
}

type nativeBindings struct {
	fx.Out
	Values []BackendBinding `group:"user_backends,flatten"`
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
			adapter := &userapi.Adapter{API: client}
			binding.Adder, binding.Remover, binding.Counter, binding.Observer = adapter, adapter, adapter, adapter
			binding.Additions.Adder = adapter
			binding.Delta.Applier = adapter
			if profile := instance.Xray.Users; profile != nil {
				binding.Additions.MaxUsers = profile.MaxBatchUsers
				binding.Delta.MaxUsers = profile.MaxBatchUsers
			}
		}
		result.Values = append(result.Values, binding)
	}
	return result, nil
}
