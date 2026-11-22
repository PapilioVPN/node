package main

import (
	"context"
	"errors"
	"os/signal"
	"syscall"

	"github.com/PapilioVPN/node/internal/apps/xray/tools/assets/infra/installation"
	"github.com/PapilioVPN/node/internal/settings"
)

func initializeAssets(config settings.Config, id, distribution string) error {
	if id == "" || distribution == "" {
		return errors.New("both -init-xray-assets and -xray-distribution are required")
	}
	for _, instance := range config.Instances {
		if instance.ID == id && instance.Xray.AssetsDirectory != "" {
			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			return installation.Initialize(ctx, id, instance.Xray.AssetsDirectory, distribution)
		}
	}
	return errors.New("configured instance with managed assets directory not found")
}
