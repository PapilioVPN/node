package domain

import (
	"time"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
)

const RemoveInboundUser = "xray.inbound.user.remove"
const AddInboundUser = "xray.inbound.user.add"

// InboundUserSelection preserves the caller's identity. Native protocols own
// email matching semantics, including case folding in several user managers.
type InboundUserSelection struct {
	InboundTag string
	Email      string
}

// UserMutationAcknowledgement confirms completion of a native operation, not
// previous existence, durable configuration or termination of existing sessions.
type UserMutationAcknowledgement struct {
	AcknowledgedAt time.Time
}

type InstanceInboundUserMutation struct {
	InstanceID      instances.InstanceID
	Selection       InboundUserSelection
	Acknowledgement UserMutationAcknowledgement
}
