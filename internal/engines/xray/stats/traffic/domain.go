// Package traffic reads upload and download counters for an active Xray instance.
package traffic

import "errors"

type Scope string

const (
	User     Scope = "user"
	Inbound  Scope = "inbound"
	Outbound Scope = "outbound"
)

func (scope Scope) valid() bool {
	switch scope {
	case User, Inbound, Outbound:
		return true
	default:
		return false
	}
}

var (
	ErrInvalid     = errors.New("invalid Xray traffic selector")
	ErrNotFound    = errors.New("Xray traffic counters not found")
	ErrBusy        = errors.New("too many concurrent Xray traffic reads")
	ErrTooLarge    = errors.New("Xray traffic response exceeds the size limit")
	ErrUnavailable = errors.New("Xray traffic counters unavailable")
)

type Usage struct {
	Name                       string
	UplinkBytes, DownlinkBytes int64
	HasUplink, HasDownlink     bool
}
