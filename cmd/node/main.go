package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/PapilioVPN/node/internal/bootstrap"
	"github.com/PapilioVPN/node/internal/buildinfo"
	"github.com/PapilioVPN/node/internal/settings"
)

func main() {
	path := flag.String("config", "config.yml", "path to the Node settings file")
	showVersion := flag.Bool("version", false, "print the Node version")
	flag.Parse()
	if *showVersion {
		fmt.Println(buildinfo.Version)
		return
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	config, err := settings.Load(*path)
	if err != nil {
		logger.Error("load settings", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err = bootstrap.Run(ctx, config, logger)
	stop()
	if err != nil {
		logger.Error("run node", "error", err)
		os.Exit(1)
	}
}
