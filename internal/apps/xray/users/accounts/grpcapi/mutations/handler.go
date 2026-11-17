// Package mutations adapts live user changes and their partial outcomes to gRPC.
package mutations

// MutationAPI owns user writes and their independent management grant.
type MutationAPI struct {
	UserRemoval         InboundUserRemover
	UserAddition        InboundUserAdder
	UserAdditions       InboundUsersAdder
	UserDelta           InboundUserDeltaApplier
	AllowUserManagement bool
}
