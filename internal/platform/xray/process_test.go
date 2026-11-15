//go:build linux

package xray_test

import (
	"errors"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/platform/xray"
)

func TestNewRequiresExplicitProcessSettings(t *testing.T) {
	for name, config := range map[string]xray.Config{
		"missing binary":  {ConfigFile: "/config/xray.json", StopTimeout: time.Second},
		"relative binary": {Binary: "xray", ConfigFile: "/config/xray.json", StopTimeout: time.Second},
		"missing config":  {Binary: "/bin/xray", StopTimeout: time.Second},
		"relative config": {Binary: "/bin/xray", ConfigFile: "xray.json", StopTimeout: time.Second},
		"missing budget":  {Binary: "/bin/xray", ConfigFile: "/config/xray.json"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := xray.New(config); !errors.Is(err, xray.ErrInvalidConfig) {
				t.Fatalf("invalid settings accepted: %v", err)
			}
		})
	}
}

func TestClosePreventsNewExecution(t *testing.T) {
	process, err := xray.New(xray.Config{
		Binary: "/unused/xray", ConfigFile: "/unused/xray.json", StopTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Close(); err != nil {
		t.Fatal(err)
	}
	if err := process.Start(t.Context()); !errors.Is(err, xray.ErrClosed) {
		t.Fatalf("execution admitted after final close: %v", err)
	}
	if err := process.Close(); err != nil {
		t.Fatalf("repeated close: %v", err)
	}
}
