package domain

import (
	"time"

	instances "github.com/PapilioVPN/node/internal/apps/core/engines/instances/domain"
)

const ReadInboundUserCount = "xray.inbound.users.count.read"

// InboundUserCount is native membership at one observation, not online users or
// an atomic snapshot with any separately obtained user list.
type InboundUserCount struct {
	Count      int64
	ObservedAt time.Time
}

type InstanceInboundUserCount struct {
	InstanceID instances.InstanceID
	InboundTag string
	Users      InboundUserCount
}
