//go:build integration && linux

package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/identity/domain"
	identityv1 "github.com/PapilioVPN/node/internal/apps/system/host/identity/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/apps/system/host/identity/infra/sys"
	"github.com/PapilioVPN/node/internal/bootstrap"
	"github.com/PapilioVPN/node/internal/testutil"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type observation struct {
	Identity struct {
		Hostname        string `json:"hostname"`
		OperatingSystem string `json:"operating_system"`
		Release         string `json:"release"`
		Version         string `json:"version"`
		Machine         string `json:"machine"`
	} `json:"identity"`
	ObservedAt time.Time `json:"observed_at"`
	DurationNS string    `json:"duration_ns"`
}

func assertObservation(t *testing.T, contents []byte, want domain.Identity, before time.Time) {
	t.Helper()
	var result observation
	if err := json.Unmarshal(contents, &result); err != nil {
		t.Fatal(err)
	}
	got := domain.Identity{Hostname: result.Identity.Hostname, OperatingSystem: result.Identity.OperatingSystem, Release: result.Identity.Release, Version: result.Identity.Version, Machine: result.Identity.Machine}
	duration, err := strconv.ParseInt(result.DurationNS, 10, 64)
	if got != want || err != nil || duration < 0 || result.ObservedAt.Before(before) || result.ObservedAt.After(time.Now()) {
		t.Fatalf("incorrect observation: %s", contents)
	}
}

func TestActualIdentityOverEachIndependentlyEnabledTransport(t *testing.T) {
	for _, transport := range []string{"http", "grpc", "nats", "all"} {
		t.Run(transport, func(t *testing.T) {
			client := startNode(t, true, transport)
			want, err := (sys.Reader{}).Read(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			before := time.Now()
			if client.HTTP != nil {
				response, err := client.HTTP.Get(client.HTTPAddress)
				if err != nil {
					t.Fatal(err)
				}
				contents, err := io.ReadAll(response.Body)
				response.Body.Close()
				if err != nil || response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "no-store" {
					t.Fatalf("HTTP read failed: %s, %v", contents, err)
				}
				assertObservation(t, contents, want, before)
				response, err = client.HTTP.Get(client.HTTPAddress + "?unknown=value")
				if err != nil {
					t.Fatal(err)
				}
				response.Body.Close()
				if response.StatusCode != http.StatusBadRequest {
					t.Fatal("unexpected input accepted")
				}
			}
			if client.GRPC != nil {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				result, err := identityv1.NewHostIdentityServiceClient(client.GRPC).GetHostIdentity(ctx, &identityv1.GetHostIdentityRequest{})
				if err != nil {
					t.Fatal(err)
				}
				got := domain.Identity{Hostname: result.Identity.Hostname, OperatingSystem: result.Identity.OperatingSystem, Release: result.Identity.Release, Version: result.Identity.Version, Machine: result.Identity.Machine}
				if got != want || result.ObservedAt.CheckValid() != nil || result.ObservedAt.AsTime().Before(before) || result.DurationNs < 0 {
					t.Fatalf("incorrect gRPC read: %v", result)
				}
			}
			if client.NATS != nil {
				message, err := client.NATS.Request(subject, []byte("{}"), time.Second)
				if err != nil {
					t.Fatal(err)
				}
				assertObservation(t, message.Data, want, before)
			}
		})
	}
}

func TestAllTransportsDenyReadsWithoutExplicitGrant(t *testing.T) {
	client := startNode(t, false, "all")
	response, err := client.HTTP.Get(client.HTTPAddress + "?invalid=value")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatal("ungranted HTTP read accepted")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = identityv1.NewHostIdentityServiceClient(client.GRPC).GetHostIdentity(ctx, &identityv1.GetHostIdentityRequest{})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("gRPC permission: %v", err)
	}
	message, err := client.NATS.Request(subject, []byte("invalid"), time.Second)
	if err != nil || string(message.Data) != `{"error":"permission_denied"}` {
		t.Fatalf("NATS permission: %v %v", message, err)
	}
}

func TestBrokerDeniedSubscriptionRequestsNodeFailure(t *testing.T) {
	config := nodeConfig(t, testutil.NewPKI(t), true, "nats")
	config.Communications.Requests.NATS.SubjectPrefix = "forbidden"
	app := bootstrap.New(config, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stop, cancel := context.WithTimeout(context.Background(), config.ShutdownTimeout)
		defer cancel()
		if err := app.Stop(stop); err == nil {
			t.Error("rejected subscription cause was lost")
		}
	})
	select {
	case stopped := <-app.Wait():
		if stopped.ExitCode != 1 {
			t.Fatal("rejected subscription did not fail Node")
		}
	case <-ctx.Done():
		t.Fatal("broker rejection left Node running with an unusable endpoint")
	}
}

func TestShortReadDeadlineDoesNotPreventCleanSubscriptionShutdown(t *testing.T) {
	pki := testutil.NewPKI(t)
	config := nodeConfig(t, pki, true, "nats")
	config.Communications.Requests.Timeout = time.Nanosecond
	client := startConfiguredNode(t, pki, config)
	if _, err := client.NATS.Request(subject, []byte("{}"), time.Second); err != nil {
		t.Fatal(err)
	}
	// Cleanup must drain/join successfully with its own budget, independently
	// of the deliberately expired application read deadline.
}
