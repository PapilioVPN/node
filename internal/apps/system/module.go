package system

import (
	"go.uber.org/fx"

	"github.com/PapilioVPN/node/internal/apps/system/app"
	"github.com/PapilioVPN/node/internal/apps/system/domain"
	"github.com/PapilioVPN/node/internal/settings"
)

// Module owns the application providers for system capabilities.
func Module(version string) fx.Option {
	return fx.Module("system", fx.Provide(func(cfg settings.Config) *app.InfoService {
		return app.NewInfoService(domain.NodeID(cfg.Node.ID), version)
	}))
}
