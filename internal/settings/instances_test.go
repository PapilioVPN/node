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
		if xray.Inspection != nil || config.Communications.GRPC.Permissions.ReadEngineInfo || config.Communications.GRPC.Permissions.ReadEngineStats {
			t.Fatal("binary inspection or its grant was implicitly enabled")
		}
		if xray.Binary != filepath.Join(filepath.Dir(path), "bin/xray") || xray.ConfigFile != filepath.Join(filepath.Dir(path), "engines", name) {
			t.Fatalf("paths not resolved against Node configuration: %+v", xray)
		}
		if xray.StopTimeout != time.Duration(i+1)*time.Second {
			t.Fatalf("per-instance stop budget lost: %v", xray.StopTimeout)
		}
	}
}

func TestLoadOptionalXrayInspection(t *testing.T) {
	path := writeConfig(t, `node: {id: west}
runtime: {startup_timeout: 3s, shutdown_timeout: 3s}
instances:
  - id: edge
    engine: xray
    xray:
      binary: bin/xray
      config_file: config.json
      stop_timeout: 1s
      inspection: {timeout: 2s, max_output_bytes: 1024, max_concurrent: 2}
communications:
  grpc:
    permissions: {read_engine_info: true}
`)
	cfg, err := settings.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	inspection := cfg.Instances[0].Xray.Inspection
	if inspection == nil || inspection.Timeout != 2*time.Second || inspection.MaxOutputBytes != 1024 || inspection.MaxConcurrent != 2 {
		t.Fatalf("inspection limits lost: %+v", inspection)
	}
	permissions := cfg.Communications.GRPC.Permissions
	if !permissions.ReadEngineInfo || permissions.ReadEngineStats || permissions.ReadInstanceStatus || permissions.ControlInstances || permissions.ValidateInstanceConfig {
		t.Fatalf("engine information expanded existing grants: %+v", permissions)
	}
}

func TestXrayInspectionRequiresExplicitBounds(t *testing.T) {
	for _, input := range []string{
		"{}",
		"{timeout: 0s, max_output_bytes: 1024, max_concurrent: 1}",
		"{timeout: 1s, max_output_bytes: -1, max_concurrent: 1}",
		"{timeout: 1s, max_output_bytes: 1024, max_concurrent: 0}",
		"{timeout: 1s, max_output_bytes: 1024, max_concurrent: 1, unknown: true}",
	} {
		_, err := settings.Load(writeConfig(t, `node: {id: west}
runtime: {startup_timeout: 3s, shutdown_timeout: 3s}
instances:
  - id: edge
    engine: xray
    xray:
      binary: bin/xray
      config_file: config.json
      stop_timeout: 1s
      inspection: `+input+"\n"))
		if !errors.Is(err, settings.ErrInvalidConfig) {
			t.Fatalf("invalid inspection settings accepted: %v", err)
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

func TestLoadOptionalConfigValidation(t *testing.T) {
	path := writeConfig(t, `node: {id: west}
runtime: {startup_timeout: 3s, shutdown_timeout: 3s}
instances:
  - id: edge-a
    engine: xray
    xray: {binary: bin/xray, config_file: engines/a.json, stop_timeout: 1s}
    validation: {work_dir: private-work, timeout: 2s, max_bytes: 1024, max_concurrent: 2}
communications:
  grpc:
    permissions: {validate_instance_config: true}
`)
	cfg, err := settings.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	validation := cfg.Instances[0].Validation
	if validation == nil || validation.WorkDir != filepath.Join(filepath.Dir(path), "private-work") || validation.Timeout != 2*time.Second || validation.MaxBytes != 1024 || validation.MaxConcurrent != 2 {
		t.Fatalf("per-instance validation settings lost: %+v", validation)
	}
	permissions := cfg.Communications.GRPC.Permissions
	if !permissions.ValidateInstanceConfig || permissions.ControlInstances || permissions.ReadInstanceStatus {
		t.Fatalf("validation expanded existing grants: %+v", permissions)
	}
}

func TestConfigValidationRequiresExplicitBounds(t *testing.T) {
	for _, input := range []string{
		"{}",
		"{work_dir: ' ', timeout: 1s, max_bytes: 16, max_concurrent: 1}",
		"{work_dir: work, timeout: 0s, max_bytes: 16, max_concurrent: 1}",
		"{work_dir: work, timeout: 1s, max_bytes: -1, max_concurrent: 1}",
		"{work_dir: work, timeout: 1s, max_bytes: 16, max_concurrent: 0}",
		"{work_dir: work, timeout: 1s, max_bytes: 16, max_concurrent: 1, unknown: true}",
	} {
		_, err := settings.Load(writeConfig(t, `node: {id: west}
runtime: {startup_timeout: 3s, shutdown_timeout: 3s}
instances:
  - id: edge
    engine: xray
    xray: {binary: xray, config_file: config.json, stop_timeout: 1s}
    validation: `+input+"\n"))
		if !errors.Is(err, settings.ErrInvalidConfig) {
			t.Fatalf("invalid validation settings accepted: %v", err)
		}
	}
}
