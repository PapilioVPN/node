//go:build integration && linux

package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/domain"
	loadv1 "github.com/PapilioVPN/node/internal/apps/system/host/cpu/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/infra/sys"
	"github.com/PapilioVPN/node/internal/testutil"
	"github.com/nats-io/nats.go"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestHostLoadOverEachIndependentlyEnabledTransport(t *testing.T) {
	// Reuse one isolated broker/PKI across all four composition cases.
	pki := testutil.NewPKI(t)
	all := nodeConfig(t, pki)
	for _, transport := range []string{"http", "grpc", "nats", "all"} {
		t.Run(transport, func(t *testing.T) {
			config := all
			if transport != "all" && transport != "http" {
				config.Communications.HTTP = nil
			}
			if transport != "all" && transport != "grpc" {
				config.Communications.GRPC = nil
			}
			if transport != "all" && transport != "nats" {
				config.Communications.NATS = nil
				config.Communications.Requests.NATS = nil
			}
			if err := config.Validate(); err != nil {
				t.Fatal(err)
			}
			client := startConfiguredNode(t, pki, config)
			assertFreshReads(t, client)
			if client.NATS != nil {
				message := nats.NewMsg(subject)
				message.Data = []byte("{}")
				message.Header.Set("Papilio-Deadline", time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano))
				reply, err := client.NATS.RequestMsg(message, time.Second)
				if err != nil || string(reply.Data) != `{"error":"deadline_exceeded"}` {
					t.Fatalf("expired NATS request: %v %v", reply, err)
				}
			}
			if client.GRPC != nil {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				_, err := loadv1.NewHostLoadServiceClient(client.GRPC).GetHostLoad(ctx, &loadv1.GetHostLoadRequest{})
				if status.Code(err) != codes.Canceled {
					t.Fatalf("canceled gRPC: %v", err)
				}
			}
		})
	}
}

func assertFreshReads(t *testing.T, client clients) {
	t.Helper()
	assertFreshTimes(t, client)
	assertSampledUsage(t, client)
	assertProcessorInfo(t, client)
	if client.HTTP != nil {
		before, lower := time.Now(), hostLoad(t)
		response, err := client.HTTP.Get(client.HTTPAddress)
		if err != nil {
			t.Fatal(err)
		}
		contents, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("HTTP load: %s %v", contents, err)
		}
		assertJSONObservation(t, contents, lower, hostLoad(t), before)
	}
	if client.GRPC != nil {
		before, lower := time.Now(), hostLoad(t)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		result, err := loadv1.NewHostLoadServiceClient(client.GRPC).GetHostLoad(ctx, &loadv1.GetHostLoadRequest{})
		if err != nil {
			t.Fatal(err)
		}
		upper := hostLoad(t)
		if result.ObservedAt.CheckValid() != nil {
			t.Fatal("invalid gRPC timestamp")
		}
		assertObservation(t, domain.Averages{OneMinute: result.OneMinute, FiveMinutes: result.FiveMinutes, FifteenMinutes: result.FifteenMinutes}, lower, upper, result.ObservedAt.AsTime(), time.Duration(result.DurationNs), before)
	}
	if client.NATS != nil {
		before, lower := time.Now(), hostLoad(t)
		message, err := client.NATS.Request(subject, []byte("{}"), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		assertJSONObservation(t, message.Data, lower, hostLoad(t), before)
	}
}

func assertJSONObservation(t *testing.T, contents []byte, lower, upper domain.Averages, before time.Time) {
	t.Helper()
	var result struct {
		OneMinute      float64   `json:"one_minute"`
		FiveMinutes    float64   `json:"five_minutes"`
		FifteenMinutes float64   `json:"fifteen_minutes"`
		ObservedAt     time.Time `json:"observed_at"`
		DurationNS     string    `json:"duration_ns"`
	}
	if err := json.Unmarshal(contents, &result); err != nil {
		t.Fatalf("invalid load JSON: %s %v", contents, err)
	}
	duration, err := strconv.ParseInt(result.DurationNS, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	assertObservation(t, domain.Averages{OneMinute: result.OneMinute, FiveMinutes: result.FiveMinutes, FifteenMinutes: result.FifteenMinutes}, lower, upper, result.ObservedAt, time.Duration(duration), before)
}

func assertObservation(t *testing.T, got, lower, upper domain.Averages, observed time.Time, duration time.Duration, before time.Time) {
	t.Helper()
	for _, window := range []struct{ value, lower, upper float64 }{
		{got.OneMinute, lower.OneMinute, upper.OneMinute},
		{got.FiveMinutes, lower.FiveMinutes, upper.FiveMinutes},
		{got.FifteenMinutes, lower.FifteenMinutes, upper.FifteenMinutes},
	} {
		if math.IsNaN(window.value) || math.IsInf(window.value, 0) || window.value < math.Min(window.lower, window.upper) || window.value > math.Max(window.lower, window.upper) {
			t.Fatalf("load %+v outside host observations %+v / %+v", got, lower, upper)
		}
	}
	if observed.Before(before) || observed.After(time.Now()) || duration < 0 || duration > time.Since(before) {
		t.Fatalf("invalid observation timing: %v %v", observed, duration)
	}
}

func hostLoad(t *testing.T) domain.Averages {
	t.Helper()
	result, err := (sys.Reader{}).Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return result
}
