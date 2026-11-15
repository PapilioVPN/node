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
	"github.com/PapilioVPN/node/internal/settings"
)

var version = "dev"

func main() {
	configPath := flag.String("config", "config.yml", "application configuration file")
	showVersion := flag.Bool("version", false, "print the binary version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}
	if err := run(*configPath); err != nil {
		slog.Error("node stopped", "error", err)
		os.Exit(1)
	}
}

func run(path string) error {
	cfg, err := settings.Load(path)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	return bootstrap.Run(ctx, cfg, version)
}
