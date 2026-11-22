//go:build integration && linux

package integration_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/bootstrap"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/testutil"
)

func TestDiagnosticsFromNormalStartup(t *testing.T) {
	binary := os.Getenv("XRAY_TEST_BINARY")
	if !filepath.IsAbs(binary) {
		t.Fatal("XRAY_TEST_BINARY must select the pinned executable")
	}
	pki := testutil.NewPKI(t)
	broker := strings.Replace(testutil.NewNATSBroker(t, pki.Server, true).ClientURL(), "nats://", "tls://", 1)
	cfg, err := settings.Load("../../../../../../deploy/config.example.yml")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Communications.HTTP.Address = "127.0.0.1:0"
	cfg.Communications.HTTP.TLS = pki.Server
	cfg.Communications.GRPC.Address = "127.0.0.1:0"
	cfg.Communications.GRPC.TLS = pki.Server
	cfg.Communications.NATS.Servers = []string{broker}
	cfg.Communications.NATS.TLS = pki.Client
	cfg.Communications.Requests.NATS.SubjectPrefix = "fixture.diagnostics"
	source := filepath.Join(t.TempDir(), "xray.json")
	if err := os.WriteFile(source, []byte(`{"log":{"loglevel":"none"},"outbounds":[{"protocol":"blackhole"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg.Instances = []settings.Instance{{ID: "edge", Engine: "xray", StopTimeout: time.Second, Xray: &settings.Xray{
		Binary: binary, Configuration: source, StateDirectory: testutil.NewPrivateDirectory(t), WorkDirectory: testutil.NewPrivateDirectory(t),
	}}}
	logs := &listenerLog{Handler: slog.NewTextHandler(io.Discard, nil), addresses: map[string]string{}}
	app := bootstrap.New(cfg, slog.New(logs))
	ctx, cancel := context.WithTimeout(context.Background(), cfg.StartupTimeout)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := app.Stop(ctx); err != nil {
			t.Error(err)
		}
	})
	f := &fixture{}
	f.connect(t, pki, broker, logs)
	for _, transport := range []string{"http", "grpc", "nats"} {
		t.Run(transport, func(t *testing.T) {
			report := f.report(t, transport)
			if report.Execution.Generation != 1 || report.Execution.LifecycleState != "running" ||
				report.Configuration.State != "absent" || report.Configuration.Reason != "no_desired_configuration" ||
				report.Configuration.DesiredRevision != nil ||
				report.Binary.Version != "26.11.9" || report.Native.State != "disabled" {
				t.Fatalf("unexpected production evidence: %+v", report)
			}
		})
	}
}
