//go:build integration && linux

package bootstrap_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/fx"
	"go.yaml.in/yaml/v3"

	"github.com/PapilioVPN/node/internal/apps/core/engines/instances"
	"github.com/PapilioVPN/node/internal/bootstrap"
	rpc "github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/testutil"
)

func TestConfiguredInstancesStartAndStop(t *testing.T) {
	certificates := testutil.NewCertificates(t)
	first, firstAddress := testutil.ConfiguredXray(t, certificates.Directory, "edge-a")
	second, secondAddress := testutil.ConfiguredXray(t, certificates.Directory, "edge-b")
	cfg := certificates.Config()
	cfg.Instances = []settings.Instance{first, second}
	// Exercise the real YAML-to-composition path, including relative engine paths.
	for i := range cfg.Instances {
		cfg.Instances[i].Xray.ConfigFile = filepath.Base(cfg.Instances[i].Xray.ConfigFile)
	}
	contents, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(certificates.Directory, "config.yml")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = settings.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	application := bootstrap.New(cfg, "test", fx.NopLogger)
	if err := application.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := application.Stop(context.Background()); err != nil {
			t.Error(err)
		}
	})
	if err := testutil.XrayTraffic(t.Context(), firstAddress, secondAddress); err != nil {
		t.Fatal(err)
	}
	if err := application.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	testutil.AssertTCPPortsReleased(t, firstAddress, secondAddress)
}

func TestInstanceStartupFailureRollsBackEarlierInstances(t *testing.T) {
	certificates := testutil.NewCertificates(t)
	first, firstAddress := testutil.ConfiguredXray(t, certificates.Directory, "edge-a")
	second, secondAddress := testutil.ConfiguredXray(t, certificates.Directory, "edge-b")
	second.Xray.Binary = filepath.Join(certificates.Directory, "missing-xray")
	cfg := certificates.Config()
	cfg.Instances = []settings.Instance{first, second}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	err := bootstrap.Run(ctx, cfg, "test", fx.NopLogger)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("native startup failure lost: %v", err)
	}
	testutil.AssertTCPPortsReleased(t, firstAddress, secondAddress)
}

func TestCanceledStartupClosesAllInstances(t *testing.T) {
	certificates := testutil.NewCertificates(t)
	first, firstAddress := testutil.ConfiguredXray(t, certificates.Directory, "edge-a")
	second, secondAddress := testutil.ConfiguredXray(t, certificates.Directory, "edge-b")
	cfg := certificates.Config()
	cfg.Instances = []settings.Instance{first, second}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	err := bootstrap.Run(ctx, cfg, "test", fx.NopLogger,
		fx.Invoke(func(lifecycle fx.Lifecycle) {
			lifecycle.Append(fx.Hook{OnStart: func(startup context.Context) error {
				if err := testutil.XrayTraffic(startup, firstAddress, secondAddress); err != nil {
					return err
				}
				cancel() // Fx cannot run rollback hooks with this canceled context.
				return context.Canceled
			}})
		}))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("startup cancellation lost: %v", err)
	}
	testutil.AssertTCPPortsReleased(t, firstAddress, secondAddress)
}

func TestExpiredStopStillAllowsFinalInstanceCleanup(t *testing.T) {
	certificates := testutil.NewCertificates(t)
	first, firstAddress := testutil.ConfiguredXray(t, certificates.Directory, "edge-a")
	second, secondAddress := testutil.ConfiguredXray(t, certificates.Directory, "edge-b")
	cfg := certificates.Config()
	cfg.Instances = []settings.Instance{first, second}
	var resources *instances.Resources
	var server *rpc.Server
	application := bootstrap.New(cfg, "test", fx.NopLogger, fx.Populate(&resources, &server))
	if err := application.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = application.Stop(context.Background())
		_ = server.Close()
		_ = resources.Close()
	})
	if err := testutil.XrayTraffic(t.Context(), firstAddress, secondAddress); err != nil {
		t.Fatal(err)
	}
	expired, cancel := context.WithCancel(t.Context())
	cancel()
	if err := application.Stop(expired); !errors.Is(err, context.Canceled) {
		t.Fatalf("expired Fx stop context lost: %v", err)
	}
	if err := resources.Close(); err != nil {
		t.Fatal(err)
	}
	testutil.AssertTCPPortsReleased(t, firstAddress, secondAddress)
}
