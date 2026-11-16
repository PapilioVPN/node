// Package engineapi declares availability and resource failures shared by engine
// capability ports. Protocol-specific failures remain with their capability.
package engineapi

import "errors"

var ErrDisabled = errors.New("engine API is not enabled")
var ErrBusy = errors.New("engine API is busy")
var ErrResourceLimit = errors.New("engine API resource limit exceeded")
