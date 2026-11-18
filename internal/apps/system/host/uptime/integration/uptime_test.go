//go:build integration && linux

package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"testing"
	"time"

	uptimev1 "github.com/PapilioVPN/node/internal/apps/system/host/uptime/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/apps/system/host/uptime/infra/sys"
)

func TestUptimeOverEachIndependentlyEnabledTransport(t *testing.T) {
	for _, transport := range []string{"http", "grpc", "nats", "all"} {
		t.Run(transport, func(t *testing.T) { assertFreshReads(t, startNode(t, transport)) })
	}
}

func assertFreshReads(t *testing.T, client clients) {
	t.Helper()
	if client.HTTP != nil {
		before, lower := time.Now(), hostUptime(t)
		response, err := client.HTTP.Get(client.HTTPAddress)
		if err != nil {
			t.Fatal(err)
		}
		contents, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("HTTP uptime: %s, %v", contents, err)
		}
		assertJSONObservation(t, contents, lower, hostUptime(t), before)
	}
	if client.GRPC != nil {
		before, lower := time.Now(), hostUptime(t)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		result, err := uptimev1.NewHostUptimeServiceClient(client.GRPC).GetHostUptime(ctx, &uptimev1.GetHostUptimeRequest{})
		upper := hostUptime(t)
		if err != nil {
			t.Fatal(err)
		}
		if result.UptimeNs < int64(lower) || result.UptimeNs > int64(upper) || result.DurationNs < 0 || result.DurationNs > int64(time.Since(before)) || result.ObservedAt.CheckValid() != nil || result.ObservedAt.AsTime().Before(before) || result.ObservedAt.AsTime().After(time.Now()) {
			t.Fatalf("incorrect gRPC observation: %v, bracket [%v, %v]", result, lower, upper)
		}
	}
	if client.NATS != nil {
		before, lower := time.Now(), hostUptime(t)
		message, err := client.NATS.Request(subject, []byte("{}"), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		assertJSONObservation(t, message.Data, lower, hostUptime(t), before)
	}
}

func assertJSONObservation(t *testing.T, contents []byte, lower, upper time.Duration, before time.Time) {
	t.Helper()
	var result struct {
		UptimeNS   string    `json:"uptime_ns"`
		ObservedAt time.Time `json:"observed_at"`
		DurationNS string    `json:"duration_ns"`
	}
	if err := json.Unmarshal(contents, &result); err != nil {
		t.Fatalf("invalid JSON observation: %s, %v", contents, err)
	}
	uptime, err := strconv.ParseInt(result.UptimeNS, 10, 64)
	if err != nil || uptime < int64(lower) || uptime > int64(upper) {
		t.Fatalf("uptime outside host bracket [%v, %v]: %s", lower, upper, contents)
	}
	duration, err := strconv.ParseInt(result.DurationNS, 10, 64)
	if err != nil || duration < 0 || duration > int64(time.Since(before)) || result.ObservedAt.Before(before) || result.ObservedAt.After(time.Now()) {
		t.Fatalf("invalid observation timing: %s", contents)
	}
}

func hostUptime(t *testing.T) time.Duration {
	t.Helper()
	result, err := (sys.Reader{}).Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return result
}
