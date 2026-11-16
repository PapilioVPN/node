// Package users composes Xray account management and observations.
package users

import (
	"errors"

	"go.uber.org/fx"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/xray/users/app/services"
	"github.com/PapilioVPN/node/internal/modules/xray/users/grpcapi"
	"github.com/PapilioVPN/node/internal/settings"
)

// BackendBinding supplies focused user capabilities of one configured instance.
// Nil ports disable their operations; the resource owner retains cleanup ownership.
type BackendBinding struct {
	InstanceID instances.InstanceID
	Adder      services.InboundUserAdder
	Remover    services.InboundUserRemover
	Counter    services.InboundUserCountReader
	Observer   services.UserObservationReader
}

type backendBindings struct {
	fx.In
	Values []BackendBinding `group:"user_backends"`
}

func Module(grpcEnabled bool) fx.Option {
	options := []fx.Option{fx.Provide(newOperations, fx.Private)}
	if grpcEnabled {
		options = append(options, fx.Provide(newUserAPI))
	}
	return fx.Module("users", options...)
}

type operations struct {
	addition     *services.UserAdditionService
	removal      *services.UserRemovalService
	counts       *services.UserCountService
	observations *services.UserObservationService
}

func newOperations(bindings backendBindings) (operations, error) {
	adders := make(map[instances.InstanceID]services.InboundUserAdder, len(bindings.Values))
	removers := make(map[instances.InstanceID]services.InboundUserRemover, len(bindings.Values))
	counters := make(map[instances.InstanceID]services.InboundUserCountReader, len(bindings.Values))
	observers := make(map[instances.InstanceID]services.UserObservationReader, len(bindings.Values))
	for _, b := range bindings.Values {
		if _, duplicate := adders[b.InstanceID]; duplicate || b.InstanceID == "" {
			return operations{}, errors.New("invalid user backend binding")
		}
		adders[b.InstanceID] = b.Adder
		removers[b.InstanceID] = b.Remover
		counters[b.InstanceID] = b.Counter
		observers[b.InstanceID] = b.Observer
	}
	return operations{
		addition:     services.NewUserAdditionService(adders),
		removal:      services.NewUserRemovalService(removers),
		counts:       services.NewUserCountService(counters),
		observations: services.NewUserObservationService(observers),
	}, nil
}

func newUserAPI(s operations, p settings.GRPCPermissions) grpcapi.UserAPI {
	return grpcapi.UserAPI{
		UserAddition: s.addition, UserRemoval: s.removal,
		UserCounts: s.counts, Users: s.observations,
		AllowUserManagement:   p.ManageEngineUsers,
		AllowUsersRead:        p.ReadEngineUsers,
		AllowUserMetadataRead: p.ReadEngineUserMetadata,
		AllowUserAccountsRead: p.ReadEngineUserAccounts,
	}
}
