package main

import (
	"errors"
	"flag"
	"os"

	"github.com/xtls/xray-core/common/cmdarg"
	"github.com/xtls/xray-core/core"
	"google.golang.org/protobuf/proto"
)

func convertConfig(args []string) error {
	if len(args) == 0 || args[0] != "pb" {
		return errors.New("expected convert pb -o output.pb source")
	}
	flags := flag.NewFlagSet("convert pb", flag.ContinueOnError)
	output := flags.String("o", "", "write the native protobuf configuration")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if *output == "" || flags.NArg() == 0 {
		return errors.New("expected a protobuf output path and configuration source")
	}
	config, err := core.LoadConfig("auto", cmdarg.Arg(flags.Args()))
	if err != nil {
		return err
	}
	data, err := proto.Marshal(config)
	if err != nil {
		return err
	}
	return os.WriteFile(*output, data, 0600)
}
