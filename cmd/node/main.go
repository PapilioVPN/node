package main

import (
	"flag"
	"log/slog"
	"os"

	"github.com/PapilioVPN/node/internal/bootstrap"
	"github.com/PapilioVPN/node/internal/settings"
)

func main() {
	path := flag.String("config", "config.yml", "Node configuration file")
	instance := flag.String("init-xray-assets", "", "initialize managed assets for this instance and exit")
	distribution := flag.String("xray-distribution", "", "directory containing trusted xray, geoip.dat and geosite.dat")
	flag.Parse()
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	config, err := settings.Load(*path)
	if err != nil {
		logger.Error("load configuration", "error", err)
		os.Exit(1)
	}
	if *instance != "" || *distribution != "" {
		if err := initializeAssets(config, *instance, *distribution); err != nil {
			logger.Error("initialize Xray assets", "error", err)
			os.Exit(1)
		}
		return
	}
	os.Exit(bootstrap.Run(config, logger))
}
