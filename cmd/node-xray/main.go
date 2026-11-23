package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/PapilioVPN/node/internal/engines/xray/stats/engine"
	"github.com/xtls/xray-core/common/cmdarg"
	"github.com/xtls/xray-core/core"
	_ "github.com/xtls/xray-core/main/distro/all"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		log.Print(err)
		if errors.Is(err, errConfigRejected) {
			os.Exit(23)
		}
		os.Exit(1)
	}
}

var errConfigRejected = errors.New("xray configuration rejected")

func run(ctx context.Context, args []string) (err error) {
	if len(args) == 1 && (args[0] == "version" || args[0] == "-version") {
		for _, line := range core.VersionStatement() {
			fmt.Println(line)
		}
		return nil
	}
	if len(args) > 0 && args[0] == "convert" {
		return convertConfig(args[1:])
	}
	if len(args) == 0 || args[0] != "run" {
		return errors.New("expected node-xray run, version, or convert pb")
	}
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	var sources cmdarg.Arg
	flags.Var(&sources, "c", "Xray configuration source")
	flags.Var(&sources, "config", "Xray configuration source")
	format := flags.String("format", "auto", "Xray configuration format")
	test := flags.Bool("test", false, "validate configuration without listening")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if len(sources) == 0 || flags.NArg() != 0 {
		return errors.New("expected configuration sources and no positional arguments")
	}
	config, err := core.LoadConfig(*format, sources)
	if err != nil {
		return errors.Join(errConfigRejected, err)
	}
	if err := engine.EnableScopeReset(config); err != nil {
		return errors.Join(errConfigRejected, err)
	}
	instance, err := core.New(config)
	if err != nil {
		return errors.Join(errConfigRejected, err)
	}
	defer func() { err = errors.Join(err, instance.Close()) }()
	if *test {
		fmt.Println("Configuration OK.")
		return nil
	}
	if err := instance.Start(); err != nil {
		return err
	}
	<-ctx.Done()
	return nil
}
