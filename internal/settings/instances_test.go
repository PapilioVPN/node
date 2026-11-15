package settings_test

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/settings"
)

func TestLoadNamedInstances(t *testing.T) {
	path := writeConfig(t, `node: {id: west}
runtime: {startup_timeout: 3s, shutdown_timeout: 3s}
instances:
  - id: edge-a
    engine: xray
    xray: {binary: bin/xray, config_file: engines/a.json, stop_timeout: 1s}
  - id: edge-b
    engine: xray
    xray: {binary: bin/xray, config_file: engines/b.json, stop_timeout: 2s}
`)
	config, err := settings.Load(path)
	if err != nil {
		t.Fatalf("valid named instances rejected: %v", err)
	}
	if len(config.Instances) != 2 || config.Instances[0].ID != "edge-a" || config.Instances[1].ID != "edge-b" {
		t.Fatalf("instance identity/order lost: %+v", config.Instances)
	}
	for i, name := range []string{"a.json", "b.json"} {
		xray := config.Instances[i].Xray
		if xray.Binary != filepath.Join(filepath.Dir(path), "bin/xray") || xray.ConfigFile != filepath.Join(filepath.Dir(path), "engines", name) {
			t.Fatalf("paths not resolved against Node configuration: %+v", xray)
		}
		if xray.StopTimeout != time.Duration(i+1)*time.Second {
			t.Fatalf("per-instance stop budget lost: %v", xray.StopTimeout)
		}
	}
}

func TestLoadRejectsInvalidInstances(t *testing.T) {
	for name, instances := range map[string]string{
		"missing identity": `[{engine: xray, xray: {binary: xray, config_file: a.json, stop_timeout: 1s}}]`,
		"blank identity":   `[{id: ' ', engine: xray, xray: {binary: xray, config_file: a.json, stop_timeout: 1s}}]`,
		"duplicate identity": `[
  {id: edge, engine: xray, xray: {binary: xray, config_file: a.json, stop_timeout: 1s}},
  {id: edge, engine: xray, xray: {binary: xray, config_file: b.json, stop_timeout: 1s}}]`,
		"missing kind":            `[{id: edge, xray: {binary: xray, config_file: a.json, stop_timeout: 1s}}]`,
		"unsupported engine":      `[{id: edge, engine: wireguard}]`,
		"missing engine settings": `[{id: edge, engine: xray}]`,
		"null engine settings":    `[{id: edge, engine: xray, xray: null}]`,
		"missing executable":      `[{id: edge, engine: xray, xray: {config_file: a.json, stop_timeout: 1s}}]`,
		"blank executable":        `[{id: edge, engine: xray, xray: {binary: ' ', config_file: a.json, stop_timeout: 1s}}]`,
		"missing config":          `[{id: edge, engine: xray, xray: {binary: xray, stop_timeout: 1s}}]`,
		"missing stop budget":     `[{id: edge, engine: xray, xray: {binary: xray, config_file: a.json}}]`,
		"negative stop budget":    `[{id: edge, engine: xray, xray: {binary: xray, config_file: a.json, stop_timeout: -1s}}]`,
		"unknown engine setting":  `[{id: edge, engine: xray, xray: {binary: xray, config_file: a.json, stop_timeout: 1s, typo: true}}]`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := settings.Load(writeConfig(t, "node: {id: west}\nruntime: {startup_timeout: 3s, shutdown_timeout: 3s}\ninstances: "+instances+"\n"))
			if !errors.Is(err, settings.ErrInvalidConfig) {
				t.Fatalf("invalid instance accepted: %v", err)
			}
		})
	}
}

func TestInstancesRequireRuntimeBudgetsWithoutTransport(t *testing.T) {
	_, err := settings.Load(writeConfig(t, `node: {id: west}
instances:
  - id: edge
    engine: xray
    xray: {binary: xray, config_file: a.json, stop_timeout: 1s}
`))
	if !errors.Is(err, settings.ErrInvalidConfig) {
		t.Fatalf("missing process-wide budgets accepted: %v", err)
	}
}
