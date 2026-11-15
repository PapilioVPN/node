package domain

import (
	"errors"
	"time"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
)

const ReadInboundUserCount = "xray.inbound.users.count.read"

var ErrInvalidInboundTag = errors.New("invalid Xray inbound tag")
var ErrUserReadUnsupported = errors.New("Xray user observations are not supported")
var ErrInvalidUserCount = errors.New("invalid Xray user count")

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
