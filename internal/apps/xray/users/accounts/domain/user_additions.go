package domain

import (
	"time"

	instances "github.com/PapilioVPN/node/internal/apps/core/engines/instances/domain"
)

const AddInboundUsers = "xray.inbound.users.add"

// InboundUserAdditions borrows an ordered collection for one exact inbound.
// Repeated identities retain the native protocol's duplicate/upsert semantics.
type InboundUserAdditions struct {
	InboundTag string
	Users      []InboundUser
}

// UserAdditionResult contains no account data. Failure is a private native cause,
// never a public error message. An acknowledgement is not durable configuration.
type UserAdditionResult struct {
	Email          string
	State          UserMutationState
	AcknowledgedAt time.Time
	Failure        error
}

// UserAdditionsReport retains input order, including effects preceding a failure.
// A missing report on pre-admission failure never implies partial execution.
type UserAdditionsReport struct {
	Items []UserAdditionResult
}

type InstanceInboundUserAdditions struct {
	InstanceID instances.InstanceID
	InboundTag string
	Report     UserAdditionsReport
}
