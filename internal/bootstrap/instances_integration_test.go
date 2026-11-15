//go:build integration && linux

package bootstrap_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/fx"
	"go.yaml.in/yaml/v3"

	"github.com/PapilioVPN/node/internal/bootstrap"
	"github.com/PapilioVPN/node/internal/modules/core/instances"
	"github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	rpc "github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/shared/testutil"
)

func TestConfiguredInstancesStartAndStop(t *testing.T) {
	certificates := testutil.NewCertificates(t)
	first, firstAddress := configuredXray(t, certificates.Directory, "edge-a")
	second, secondAddress := configuredXray(t, certificates.Directory, "edge-b")
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
	if err := xrayTraffic(t.Context(), firstAddress, secondAddress); err != nil {
		t.Fatal(err)
	}
	if err := application.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertInstancePortsReleased(t, firstAddress, secondAddress)
}

func TestInstanceStartupFailureRollsBackEarlierInstances(t *testing.T) {
	certificates := testutil.NewCertificates(t)
	first, firstAddress := configuredXray(t, certificates.Directory, "edge-a")
	second, secondAddress := configuredXray(t, certificates.Directory, "edge-b")
	second.Xray.Binary = filepath.Join(certificates.Directory, "missing-xray")
	cfg := certificates.Config()
	cfg.Instances = []settings.Instance{first, second}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	err := bootstrap.Run(ctx, cfg, "test", fx.NopLogger)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("native startup failure lost: %v", err)
	}
	assertInstancePortsReleased(t, firstAddress, secondAddress)
}

func TestCanceledStartupClosesAllInstances(t *testing.T) {
	certificates := testutil.NewCertificates(t)
	first, firstAddress := configuredXray(t, certificates.Directory, "edge-a")
	second, secondAddress := configuredXray(t, certificates.Directory, "edge-b")
	cfg := certificates.Config()
	cfg.Instances = []settings.Instance{first, second}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	err := bootstrap.Run(ctx, cfg, "test", fx.NopLogger,
		fx.Invoke(func(lifecycle fx.Lifecycle) {
			lifecycle.Append(fx.Hook{OnStart: func(startup context.Context) error {
				if err := xrayTraffic(startup, firstAddress, secondAddress); err != nil {
					return err
				}
				cancel() // Fx cannot run rollback hooks with this canceled context.
				return context.Canceled
			}})
		}))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("startup cancellation lost: %v", err)
	}
	assertInstancePortsReleased(t, firstAddress, secondAddress)
}

func TestExpiredStopStillAllowsFinalInstanceCleanup(t *testing.T) {
	certificates := testutil.NewCertificates(t)
	first, firstAddress := configuredXray(t, certificates.Directory, "edge-a")
	second, secondAddress := configuredXray(t, certificates.Directory, "edge-b")
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
	if err := xrayTraffic(t.Context(), firstAddress, secondAddress); err != nil {
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
	assertInstancePortsReleased(t, firstAddress, secondAddress)
}

func configuredXray(t *testing.T, directory, id string) (settings.Instance, string) {
	t.Helper()
	binary := os.Getenv("XRAY_TEST_BINARY")
	if binary == "" {
		t.Fatal("XRAY_TEST_BINARY is required; run make integration")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf(`{"log":{"loglevel":"none"},"inbounds":[{"listen":"127.0.0.1","port":%d,"protocol":"http","settings":{}}],"outbounds":[{"protocol":"freedom"}]}`, port)
	path := filepath.Join(directory, id+".json")
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	return settings.Instance{ID: domain.InstanceID(id), Engine: domain.Xray,
		Xray: &settings.Xray{Binary: binary, ConfigFile: path, StopTimeout: time.Second}}, address
}

func xrayTraffic(ctx context.Context, addresses ...string) error {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "configured-instance")
	}))
	defer target.Close()
	for _, address := range addresses {
		proxy, err := url.Parse("http://" + address)
		if err != nil {
			return err
		}
		transport := &http.Transport{Proxy: http.ProxyURL(proxy)}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, Timeout: 200 * time.Millisecond}
		deadline, cancel := context.WithTimeout(ctx, 5*time.Second)
		ticker := time.NewTicker(10 * time.Millisecond)
		for {
			request, err := http.NewRequestWithContext(deadline, http.MethodGet, target.URL, nil)
			if err != nil {
				ticker.Stop()
				cancel()
				return err
			}
			response, err := client.Do(request)
			if err == nil {
				body, readErr := io.ReadAll(io.LimitReader(response.Body, 64))
				response.Body.Close()
				if readErr == nil && response.StatusCode == http.StatusOK && string(body) == "configured-instance" {
					break
				}
			}
			select {
			case <-deadline.Done():
				ticker.Stop()
				cancel()
				return fmt.Errorf("configured instance %s did not proxy local traffic: %w", address, deadline.Err())
			case <-ticker.C:
			}
		}
		ticker.Stop()
		cancel()
	}
	return nil
}

func assertInstancePortsReleased(t *testing.T, addresses ...string) {
	t.Helper()
	for _, address := range addresses {
		listener, err := net.Listen("tcp", address)
		if err != nil {
			t.Fatalf("instance listener leaked: %v", err)
		}
		listener.Close()
	}
}
