// Package users composes Xray account management and observations.
package users

import (
	"errors"

	"go.uber.org/fx"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	system "github.com/PapilioVPN/node/internal/modules/system/info/domain"
	"github.com/PapilioVPN/node/internal/modules/xray/users/app/services"
	"github.com/PapilioVPN/node/internal/modules/xray/users/domain"
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
	Online     services.OnlineReader
}

type backendBindings struct {
	fx.In
	Values []BackendBinding `group:"user_backends"`
}

func Module(grpcEnabled bool) fx.Option {
	options := []fx.Option{fx.Provide(newOperations, fx.Private), fx.Provide(capability)}
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
	online       *services.OnlineService
}

func newOperations(bindings backendBindings) (operations, error) {
	adders := make(map[instances.InstanceID]services.InboundUserAdder, len(bindings.Values))
	removers := make(map[instances.InstanceID]services.InboundUserRemover, len(bindings.Values))
	counters := make(map[instances.InstanceID]services.InboundUserCountReader, len(bindings.Values))
	observers := make(map[instances.InstanceID]services.UserObservationReader, len(bindings.Values))
	online := make(map[instances.InstanceID]services.OnlineReader, len(bindings.Values))
	for _, b := range bindings.Values {
		if _, duplicate := adders[b.InstanceID]; duplicate || b.InstanceID == "" {
			return operations{}, errors.New("invalid user backend binding")
		}
		adders[b.InstanceID] = b.Adder
		removers[b.InstanceID] = b.Remover
		counters[b.InstanceID] = b.Counter
		observers[b.InstanceID] = b.Observer
		online[b.InstanceID] = b.Online
	}
	return operations{
		addition:     services.NewUserAdditionService(adders),
		removal:      services.NewUserRemovalService(removers),
		counts:       services.NewUserCountService(counters),
		observations: services.NewUserObservationService(observers),
		online:       services.NewOnlineService(online),
	}, nil
}

func newUserAPI(s operations, p settings.GRPCPermissions) grpcapi.UserAPI {
	return grpcapi.UserAPI{
		UserAddition: s.addition, UserRemoval: s.removal,
		UserCounts: s.counts, Users: s.observations,
		Online: s.online, AllowOnlineRead: p.ReadEngineOnline,
		AllowUserManagement:   p.ManageEngineUsers,
		AllowUsersRead:        p.ReadEngineUsers,
		AllowUserMetadataRead: p.ReadEngineUserMetadata,
		AllowUserAccountsRead: p.ReadEngineUserAccounts,
	}
}

type capabilityResult struct {
	fx.Out
	Values []system.Capability `group:"node_capabilities,flatten"`
}

func capability() capabilityResult {
	return capabilityResult{Values: []system.Capability{
		domain.AddInboundUser, domain.RemoveInboundUser, domain.ReadInboundUserCount,
		domain.ReadInboundUser, domain.ListInboundUsers,
		domain.ReadInboundUserMetadata, domain.ListInboundUserMetadata,
		domain.ReadUserOnlineStats, domain.ListUserOnlineIPs, domain.ListOnlineUsers,
	}}
}
