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
	flag.Parse()
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	config, err := settings.Load(*path)
	if err != nil {
		logger.Error("load configuration", "error", err)
		os.Exit(1)
	}
	bootstrap.New(config, logger).Run()
}
