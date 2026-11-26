// Package fault defines management errors shared by Hysteria capabilities.
package fault

import "errors"

var (
	ErrNotFound    = errors.New("Hysteria instance not found")
	ErrInvalid     = errors.New("invalid Hysteria request")
	ErrTooLarge    = errors.New("Hysteria request is too large")
	ErrBusy        = errors.New("Hysteria instance is busy")
	ErrInactive    = errors.New("Hysteria execution is not ready")
	ErrUnavailable = errors.New("Hysteria execution unavailable")
)
