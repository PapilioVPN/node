package domain

import "errors"

// InboundUserSet is the complete desired collection for one selected inbound.
// An empty Users collection clears that inbound; unselected inbounds are retained.
type InboundUserSet struct {
	InboundTag string
	Users      []InboundUser
}

var ErrInvalidUserConfiguration = errors.New("invalid Xray user configuration")
var ErrUserConfigurationTooLarge = errors.New("Xray user configuration exceeds its configured limit")
