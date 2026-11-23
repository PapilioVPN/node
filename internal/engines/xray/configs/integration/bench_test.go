package integration_test

import (
	"bytes"
	"errors"
	"os/exec"
	"testing"

	configsv1 "github.com/PapilioVPN/node/internal/engines/xray/configs/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/xray/configs/runconfig"
)

func BenchmarkValidateConfig(b *testing.B) {
	for _, config := range []struct {
		name, json string
		valid      bool
	}{
		{"valid", `{"log":{"loglevel":"none"},"outbounds":[{"protocol":"freedom"}]}`, true},
		{"rejected", `{"outbounds":[{"protocol":"unknown-fixture-protocol"}]}`, false},
	} {
		b.Run(config.name, func(b *testing.B) {
			f := newFixture(b, config.json)
			request := &configsv1.ValidateXrayConfigRequest{Id: instanceID}
			b.Run("native", func(b *testing.B) {
				run, err := runconfig.Prepare(f.config.ConfigFile, f.config.RuntimeDirectory)
				require(b, err)
				b.Cleanup(func() { require(b, run.Cleanup()) })
				args := append(run.Args, "-test")
				b.ReportAllocs()
				for b.Loop() {
					cmd := exec.CommandContext(b.Context(), f.config.Executable, args...)
					cmd.Stdin = bytes.NewReader(run.Overlay)
					err := cmd.Run()
					var exit *exec.ExitError
					if config.valid && err != nil || !config.valid && (!errors.As(err, &exit) || exit.ExitCode() != 23) {
						b.Fatalf("native validation error = %v, want valid=%v", err, config.valid)
					}
				}
			})
			b.Run("http", func(b *testing.B) {
				if got := postValidation(b, f, b.Context()); got.Valid != config.valid {
					b.Fatalf("HTTP validation result = %+v", got)
				}
				b.ReportAllocs()
				for b.Loop() {
					if got := postValidation(b, f, b.Context()); got.Valid != config.valid {
						b.Fatalf("HTTP validation result = %+v", got)
					}
				}
			})
			b.Run("grpc", func(b *testing.B) {
				check := func() {
					result, err := f.configs.ValidateConfig(b.Context(), request)
					if err != nil || result.GetValid() != config.valid {
						b.Fatalf("gRPC validation = (%v, %v)", result, err)
					}
				}
				check()
				b.ReportAllocs()
				for b.Loop() {
					check()
				}
			})
		})
	}
}
