// Package storage composes implemented filesystem and device observations.
package storage

import (
	"github.com/PapilioVPN/node/internal/apps/system/storage/devices"
	"github.com/PapilioVPN/node/internal/apps/system/storage/filesystems"
	"github.com/PapilioVPN/node/internal/settings"
	"go.uber.org/fx"
)

func Module(requests settings.Requests) fx.Option {
	return fx.Module("storage", filesystems.Module(requests), devices.Module(requests))
}
