// Package traffic reads byte counters from the active Xray execution.
package traffic

import (
	"errors"
	"iter"
)

type Scope string

const (
	User     Scope = "user"
	Inbound  Scope = "inbound"
	Outbound Scope = "outbound"
)

var (
	ErrUnavailable = errors.New("Xray traffic counters unavailable")
	ErrInvalid     = errors.New("invalid Xray traffic selector")
	ErrNotFound    = errors.New("Xray traffic counters not found")
	ErrBusy        = errors.New("too many concurrent Xray traffic reads")
	ErrTooLarge    = errors.New("Xray traffic response exceeds the size limit")
)

type Usage struct {
	Name                       string
	UplinkBytes, DownlinkBytes int64
	HasUplink, HasDownlink     bool
}

type Counters struct {
	Count int
	Each  iter.Seq2[string, int64]
}
