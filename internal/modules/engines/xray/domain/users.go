package domain

import (
	"errors"
	"time"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
)

const RemoveInboundUser = "xray.inbound.user.remove"
const AddInboundUser = "xray.inbound.user.add"

var ErrInvalidUserSelection = errors.New("invalid Xray user selection")
var ErrUserManagementUnsupported = errors.New("Xray user management is not supported")
var ErrUserMutationUnknown = errors.New("Xray user mutation outcome is unknown")
var ErrInvalidUserAccount = errors.New("invalid Xray user account")
var ErrInboundNotFound = errors.New("Xray inbound not found")
var ErrInboundProtocolMismatch = errors.New("Xray inbound protocol does not match account")
var ErrInvalidInboundResponse = errors.New("invalid Xray inbound response")

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
