// Package tools composes Xray's native utility capabilities.
package tools

import (
	"github.com/PapilioVPN/node/internal/apps/xray/tools/assets"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/credentials"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/diagnostics"
	"github.com/PapilioVPN/node/internal/settings"
	"go.uber.org/fx"
)

func Module(config []settings.Instance, requests settings.Requests) fx.Option {
	options := []fx.Option{credentials.Module(config, requests), diagnostics.Module(config, requests)}
	var managed []assets.Config
	for _, instance := range config {
		if instance.Xray != nil && instance.Xray.AssetsDirectory != "" {
			managed = append(managed, assets.Config{ID: instance.ID, Directory: instance.Xray.AssetsDirectory})
		}
	}
	if len(managed) != 0 {
		options = append(options, assets.Module(managed, requests))
	}
	return fx.Module("xray-tools", options...)
}
