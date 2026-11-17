package domain

import (
	"time"

	instances "github.com/PapilioVPN/node/internal/apps/core/engines/instances/domain"
)

const ReadInboundUserMetadata = "xray.inbound.user.metadata.read"
const ListInboundUserMetadata = "xray.inbound.users.metadata.list"

const ReadInboundUser = "xray.inbound.user.read"
const ListInboundUsers = "xray.inbound.users.list"

// ObservedUserAccount owns the original native account protobuf bytes, including
// extensions. TypeName is Xray's fully qualified message name, not a dispatch key
// for Node operations. This preserves fields outside AddInboundUser's input DTO.
type ObservedUserAccount struct {
	TypeName string
	Protobuf []byte
}

// ObservedInboundUser is a user-manager record, not the original configuration or
// proof of all data-plane settings. For example, native Shadowsocks omits IVCheck
// on readback; a WireGuard record can omit a PSK still installed in its device.
type ObservedInboundUser struct {
	Email   string
	Level   uint32
	Account ObservedUserAccount
}

// InboundUserObservation distinguishes a completed absent lookup from failed I/O.
// Native protocols own email matching; the returned stored email may differ.
// Managers allowing duplicate emails choose a record; this lookup does not prove
// email uniqueness and does not list other members to establish it.
type InboundUserObservation struct {
	User       *ObservedInboundUser
	ObservedAt time.Time
}

type InboundUsersObservation struct {
	Users      []ObservedInboundUser
	ObservedAt time.Time
}

type InstanceInboundUserObservation struct {
	InstanceID  instances.InstanceID
	Selection   InboundUserSelection
	Observation InboundUserObservation
}

type InstanceInboundUsersObservation struct {
	InstanceID  instances.InstanceID
	InboundTag  string
	Observation InboundUsersObservation
}
