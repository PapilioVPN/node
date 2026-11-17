//go:build linux

package process_test

import (
	"errors"
	"testing"
	"time"

	xrayprocess "github.com/PapilioVPN/node/internal/apps/core/engines/instances/infra/xray/process"
)

func TestNewRequiresExplicitProcessSettings(t *testing.T) {
	for name, config := range map[string]xrayprocess.Config{
		"missing binary":  {ConfigFile: "/config/xray.json", StopTimeout: time.Second},
		"relative binary": {Binary: "xray", ConfigFile: "/config/xray.json", StopTimeout: time.Second},
		"missing config":  {Binary: "/bin/xray", StopTimeout: time.Second},
		"relative config": {Binary: "/bin/xray", ConfigFile: "xray.json", StopTimeout: time.Second},
		"missing budget":  {Binary: "/bin/xray", ConfigFile: "/config/xray.json"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := xrayprocess.New(config); !errors.Is(err, xrayprocess.ErrInvalidConfig) {
				t.Fatalf("invalid settings accepted: %v", err)
			}
		})
	}
}

func TestClosePreventsNewExecution(t *testing.T) {
	process, err := xrayprocess.New(xrayprocess.Config{
		Binary: "/unused/xray", ConfigFile: "/unused/xray.json", StopTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Close(); err != nil {
		t.Fatal(err)
	}
	if err := process.Start(t.Context()); !errors.Is(err, xrayprocess.ErrClosed) {
		t.Fatalf("execution admitted after final close: %v", err)
	}
	if err := process.Close(); err != nil {
		t.Fatalf("repeated close: %v", err)
	}
}
