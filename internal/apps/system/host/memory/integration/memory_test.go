//go:build integration && linux

package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/memory/domain"
	memoryv1 "github.com/PapilioVPN/node/internal/apps/system/host/memory/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/testutil"
	"github.com/nats-io/nats.go"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestHostMemoryOverEachIndependentlyEnabledTransport(t *testing.T) {
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
				_, err := memoryv1.NewHostMemoryServiceClient(client.GRPC).GetHostMemory(ctx, &memoryv1.GetHostMemoryRequest{})
				if status.Code(err) != codes.Canceled {
					t.Fatalf("canceled gRPC: %v", err)
				}
			}
		})
	}
}

func assertFreshReads(t *testing.T, client clients) {
	t.Helper()
	if client.HTTP != nil {
		before := time.Now()
		response, err := client.HTTP.Get(client.HTTPAddress)
		if err != nil {
			t.Fatal(err)
		}
		contents, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("HTTP memory: %s %v", contents, err)
		}
		assertJSONObservation(t, contents, before)
	}
	if client.GRPC != nil {
		before := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		result, err := memoryv1.NewHostMemoryServiceClient(client.GRPC).GetHostMemory(ctx, &memoryv1.GetHostMemoryRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if result.ObservedAt.CheckValid() != nil {
			t.Fatal("invalid gRPC timestamp")
		}
		assertObservation(t, domain.Snapshot{TotalBytes: result.TotalBytes, AvailableBytes: result.AvailableBytes, UsedBytes: result.UsedBytes, FreeBytes: result.FreeBytes, ActiveBytes: result.ActiveBytes, InactiveBytes: result.InactiveBytes, BuffersBytes: result.BuffersBytes, CachedBytes: result.CachedBytes, SharedBytes: result.SharedBytes, SlabBytes: result.SlabBytes, UsedPercent: result.UsedPercent}, result.ObservedAt.AsTime(), time.Duration(result.DurationNs), before)
	}
	if client.NATS != nil {
		before := time.Now()
		message, err := client.NATS.Request(subject, []byte("{}"), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		assertJSONObservation(t, message.Data, before)
	}
}

func assertJSONObservation(t *testing.T, contents []byte, before time.Time) {
	t.Helper()
	var result struct {
		TotalBytes     uint64    `json:"total_bytes,string"`
		AvailableBytes uint64    `json:"available_bytes,string"`
		UsedBytes      uint64    `json:"used_bytes,string"`
		FreeBytes      uint64    `json:"free_bytes,string"`
		ActiveBytes    uint64    `json:"active_bytes,string"`
		InactiveBytes  uint64    `json:"inactive_bytes,string"`
		BuffersBytes   uint64    `json:"buffers_bytes,string"`
		CachedBytes    uint64    `json:"cached_bytes,string"`
		SharedBytes    uint64    `json:"shared_bytes,string"`
		SlabBytes      uint64    `json:"slab_bytes,string"`
		UsedPercent    float64   `json:"used_percent"`
		ObservedAt     time.Time `json:"observed_at"`
		DurationNS     int64     `json:"duration_ns,string"`
	}
	if err := json.Unmarshal(contents, &result); err != nil {
		t.Fatalf("invalid memory JSON: %s %v", contents, err)
	}
	assertObservation(t, domain.Snapshot{TotalBytes: result.TotalBytes, AvailableBytes: result.AvailableBytes, UsedBytes: result.UsedBytes, FreeBytes: result.FreeBytes, ActiveBytes: result.ActiveBytes, InactiveBytes: result.InactiveBytes, BuffersBytes: result.BuffersBytes, CachedBytes: result.CachedBytes, SharedBytes: result.SharedBytes, SlabBytes: result.SlabBytes, UsedPercent: result.UsedPercent}, result.ObservedAt, time.Duration(result.DurationNS), before)
}

func assertObservation(t *testing.T, got domain.Snapshot, observed time.Time, duration time.Duration, before time.Time) {
	t.Helper()
	if got.TotalBytes != hostTotal(t) || got.AvailableBytes > got.TotalBytes || got.FreeBytes > got.TotalBytes || got.UsedBytes != got.TotalBytes-got.AvailableBytes || math.IsNaN(got.UsedPercent) || math.IsInf(got.UsedPercent, 0) || math.Abs(got.UsedPercent-float64(got.UsedBytes)/float64(got.TotalBytes)*100) > 1e-9 {
		t.Fatalf("incorrect system-wide memory observation: %+v", got)
	}
	if observed.Before(before) || observed.After(time.Now()) || duration < 0 || duration > time.Since(before) {
		t.Fatalf("invalid observation timing: %v %v", observed, duration)
	}
}

func hostTotal(t *testing.T) uint64 {
	t.Helper()
	var info unix.Sysinfo_t
	if err := unix.Sysinfo(&info); err != nil {
		t.Fatal(err)
	}
	return uint64(info.Totalram) * uint64(info.Unit)
}
