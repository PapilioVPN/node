package domain

import (
	"errors"
	"time"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
)

const ReadUserOnlineStats = "xray.user.online.read"
const ListUserOnlineIPs = "xray.user.online.ips.list"
const ListOnlineUsers = "xray.users.online.list"

var ErrOnlineObservationNotFound = errors.New("Xray online observation not found")
var ErrOnlineUnsupported = errors.New("Xray online observations are not supported")
var ErrInvalidOnlineResponse = errors.New("invalid Xray online response")
var ErrInvalidUserEmail = errors.New("Xray user email is required")

type UserEmail string

// UserOnline counts distinct IPs tracked by native engine session contexts.
// Session cleanup can lag client socket closure; this is not a presence guarantee.
// Absence of a registered native map is an error, not a zero observation.
type UserOnline struct {
	OnlineIPCount int64
	ObservedAt    time.Time
}

type OnlineIP struct {
	IP string
	// LastSeenUnixSeconds is the native last AddIP time, not a traffic heartbeat.
	LastSeenUnixSeconds int64
}

type UserOnlineIPs struct {
	Items      []OnlineIP
	ObservedAt time.Time
}

type OnlineUsers struct {
	Emails     []UserEmail
	ObservedAt time.Time
}

type InstanceUserOnline struct {
	InstanceID instances.InstanceID
	Email      UserEmail
	Online     UserOnline
}

type InstanceUserOnlineIPs struct {
	InstanceID instances.InstanceID
	Email      UserEmail
	IPs        UserOnlineIPs
}

type InstanceOnlineUsers struct {
	InstanceID instances.InstanceID
	Users      OnlineUsers
}
