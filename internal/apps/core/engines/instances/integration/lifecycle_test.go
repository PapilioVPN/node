//go:build integration && linux

package integration_test

import (
	"bytes"
	"context"
	instancesv1 "github.com/PapilioVPN/node/internal/apps/core/engines/instances/grpcapi/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/testutil"
)

func TestNativeLifecycleOverEveryIndependentTransport(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "instance-traffic-proof") }))
	defer target.Close()
	pki := testutil.NewPKI(t)
	broker := brokerFixture(t, pki)
	for _, transport := range []string{"http", "grpc", "nats"} {
		t.Run(transport, func(t *testing.T) {
			first, second := engineFixture(t, "edge-a"), engineFixture(t, "edge-b")
			client := startNode(t, pki, nodeConfig(t, pki, broker, transport, first, second))
			exerciseLifecycle(t, client, transport, first, second, target.URL)
		})
	}
}

func exerciseLifecycle(t *testing.T, client clients, transport string, first, second engine, target string) {
	t.Helper()
	awaitReady(t, client, transport, "edge-a")
	awaitReady(t, client, transport, "edge-b")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	initial, err := client.status(ctx, transport, "edge-a")
	if err != nil || initial.State != "running" {
		t.Fatal(initial, err)
	}
	generation, err := strconv.ParseUint(initial.Generation, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	if err := traffic(first.proxy, target); err != nil {
		t.Fatal(err)
	}
	if err := traffic(second.proxy, target); err != nil {
		t.Fatal(err)
	}
	already, err := client.control(ctx, transport, "start", "edge-a")
	if err != nil || already.Status.Generation != initial.Generation {
		t.Fatal("start duplicated execution", already, err)
	}
	stopped, err := client.control(ctx, transport, "stop", "edge-a")
	if err != nil || stopped.Status.State != "stopped" || stopped.Effect != "completed" {
		t.Fatal(stopped, err)
	}
	if err := traffic(first.proxy, target); err == nil {
		t.Fatal("stopped engine accepted traffic")
	}
	if err := traffic(second.proxy, target); err != nil {
		t.Fatal("other instance affected", err)
	}
	started, err := client.control(ctx, transport, "start", "edge-a")
	if err != nil || started.Status.Generation != strconv.FormatUint(generation+1, 10) {
		t.Fatal(started, err)
	}
	awaitReady(t, client, transport, "edge-a")
	restarted, err := client.control(ctx, transport, "restart", "edge-a")
	if err != nil || restarted.Status.Generation != strconv.FormatUint(generation+2, 10) || restarted.Effect != "completed" {
		t.Fatal(restarted, err)
	}
	awaitReady(t, client, transport, "edge-a")
	if err := traffic(first.proxy, target); err != nil {
		t.Fatal(err)
	}
	other, err := client.status(ctx, transport, "edge-b")
	if err != nil || other.Generation != "1" || other.State != "running" {
		t.Fatal(other, err)
	}
	for _, engine := range []engine{first, second} {
		data, err := os.ReadFile(engine.settings.Xray.Configuration)
		if err != nil || !bytes.Equal(data, engine.original) {
			t.Fatal("engine configuration changed", err)
		}
	}
}

func TestNativeInvalidConfigurationExitIsAnObservation(t *testing.T) {
	pki := testutil.NewPKI(t)
	engine := engineFixture(t, "broken")
	if err := os.WriteFile(engine.settings.Xray.Configuration, []byte(`{"unknown_invalid_config":`), 0600); err != nil {
		t.Fatal(err)
	}
	client := startNode(t, pki, nodeConfig(t, pki, "", "grpc", engine))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		got, err := client.status(ctx, "grpc", "broken")
		if err != nil {
			t.Fatal(err)
		}
		if got.State == "exited" {
			if got.ExitCode == nil || *got.ExitCode != 23 || !got.ExecutionFailed {
				t.Fatalf("exit evidence: %+v", got)
			}
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("native exit not observed")
		case <-ticker.C:
		}
	}
}

func TestRealRestartFailureAndLostReplyDoNotHideEffects(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "instance-traffic-proof") }))
	defer target.Close()
	pki := testutil.NewPKI(t)
	engine := engineFixture(t, "edge-a")
	alias := filepath.Join(t.TempDir(), "xray")
	if err := os.Symlink(engine.settings.Xray.Binary, alias); err != nil {
		t.Fatal(err)
	}
	engine.settings.Xray.Binary = alias
	client := startNode(t, pki, nodeConfig(t, pki, "", "grpc", engine))
	awaitReady(t, client, "grpc", "edge-a")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	native := instancesv1.NewInstancesServiceClient(client.grpc)
	// The caller discards a valid mutation reply. Its receive error is not proof
	// that the restart was not applied, and must never trigger automatic replay.
	_, err := native.RestartInstance(ctx, &instancesv1.RestartInstanceRequest{InstanceId: "edge-a"}, grpc.MaxCallRecvMsgSize(1))
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("expected discarded reply: %v", err)
	}
	awaitReady(t, client, "grpc", "edge-a")
	observed, err := client.status(ctx, "grpc", "edge-a")
	if err != nil || observed.Generation != "2" {
		t.Fatal(observed, err)
	}
	if err := traffic(engine.proxy, target.URL); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	_, err = native.RestartInstance(ctx, &instancesv1.RestartInstanceRequest{InstanceId: "edge-a"})
	if status.Code(err) != codes.Unavailable {
		t.Fatal(err)
	}
	details := status.Convert(err).Details()
	if len(details) != 1 {
		t.Fatal("missing partial restart result")
	}
	result, ok := details[0].(*instancesv1.ControlResult)
	if !ok || result.Effect != "partial" || result.Status.State != "stopped" || result.Status.Generation != 2 {
		t.Fatalf("restart progress: %v", details)
	}
	if err := traffic(engine.proxy, target.URL); err == nil {
		t.Fatal("failed restart retained traffic unexpectedly")
	}
}
