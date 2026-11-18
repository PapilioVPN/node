//go:build integration && linux

package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/memory/domain"
	memoryv1 "github.com/PapilioVPN/node/internal/apps/system/host/memory/grpcapi/proto/v1"
	"github.com/nats-io/nats.go"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const swapSubject = "fixture.node-a.system.host.swap.v1.get"

func assertFreshSwapReads(t *testing.T, client clients) {
	t.Helper()
	if client.HTTP != nil {
		before := readHostSwap(t)
		started := time.Now()
		response, err := client.HTTP.Get(strings.TrimSuffix(client.HTTPAddress, "/memory") + "/swap")
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("HTTP swap: %s %v", body, err)
		}
		assertSwapJSON(t, body, started, before)
	}
	if client.GRPC != nil {
		before := readHostSwap(t)
		started := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		result, err := memoryv1.NewHostMemoryServiceClient(client.GRPC).GetHostSwap(ctx, &memoryv1.GetHostSwapRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if result.ObservedAt.CheckValid() != nil {
			t.Fatal("invalid swap timestamp")
		}
		assertSwapObservation(t, domain.SwapSnapshot{TotalBytes: result.TotalBytes, UsedBytes: result.UsedBytes, FreeBytes: result.FreeBytes, SwappedInBytes: result.SwappedInBytes, SwappedOutBytes: result.SwappedOutBytes, UsedPercent: result.UsedPercent}, result.ObservedAt.AsTime(), time.Duration(result.DurationNs), started, before)
		canceled, stop := context.WithCancel(context.Background())
		stop()
		if _, err := memoryv1.NewHostMemoryServiceClient(client.GRPC).GetHostSwap(canceled, &memoryv1.GetHostSwapRequest{}); status.Code(err) != codes.Canceled {
			t.Fatalf("canceled swap: %v", err)
		}
	}
	if client.NATS != nil {
		before := readHostSwap(t)
		started := time.Now()
		message, err := client.NATS.Request(swapSubject, []byte("{}"), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		assertSwapJSON(t, message.Data, started, before)
		expired := nats.NewMsg(swapSubject)
		expired.Data = []byte("{}")
		expired.Header.Set("Papilio-Deadline", time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano))
		reply, err := client.NATS.RequestMsg(expired, time.Second)
		if err != nil || string(reply.Data) != `{"error":"deadline_exceeded"}` {
			t.Fatalf("expired swap: %v %v", reply, err)
		}
	}
}

func assertSwapJSON(t *testing.T, body []byte, started time.Time, before domain.SwapSnapshot) {
	t.Helper()
	var result struct {
		TotalBytes      uint64    `json:"total_bytes,string"`
		UsedBytes       uint64    `json:"used_bytes,string"`
		FreeBytes       uint64    `json:"free_bytes,string"`
		SwappedInBytes  uint64    `json:"swapped_in_bytes,string"`
		SwappedOutBytes uint64    `json:"swapped_out_bytes,string"`
		UsedPercent     float64   `json:"used_percent"`
		ObservedAt      time.Time `json:"observed_at"`
		DurationNS      int64     `json:"duration_ns,string"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("invalid swap JSON: %s %v", body, err)
	}
	assertSwapObservation(t, domain.SwapSnapshot{TotalBytes: result.TotalBytes, UsedBytes: result.UsedBytes, FreeBytes: result.FreeBytes, SwappedInBytes: result.SwappedInBytes, SwappedOutBytes: result.SwappedOutBytes, UsedPercent: result.UsedPercent}, result.ObservedAt, time.Duration(result.DurationNS), started, before)
}

func assertSwapObservation(t *testing.T, got domain.SwapSnapshot, observed time.Time, duration time.Duration, started time.Time, before domain.SwapSnapshot) {
	t.Helper()
	after := readHostSwap(t)
	if got.TotalBytes != before.TotalBytes || got.TotalBytes != after.TotalBytes || got.FreeBytes > got.TotalBytes || got.UsedBytes != got.TotalBytes-got.FreeBytes || got.SwappedInBytes < before.SwappedInBytes || got.SwappedInBytes > after.SwappedInBytes || got.SwappedOutBytes < before.SwappedOutBytes || got.SwappedOutBytes > after.SwappedOutBytes {
		t.Fatalf("swap outside native bounds: before=%+v got=%+v after=%+v", before, got, after)
	}
	percent := 0.0
	if got.TotalBytes > 0 {
		percent = float64(got.UsedBytes) / float64(got.TotalBytes) * 100
	}
	if math.IsNaN(got.UsedPercent) || math.IsInf(got.UsedPercent, 0) || math.Abs(got.UsedPercent-percent) > 1e-9 {
		t.Fatalf("invalid swap percent: %+v", got)
	}
	if observed.Before(started) || observed.After(time.Now()) || duration < 0 || duration > time.Since(started) {
		t.Fatalf("invalid swap timing: %v %v", observed, duration)
	}
}

// Read independent outside-Node kernel facts to bracket all public observations.
func readHostSwap(t *testing.T) domain.SwapSnapshot {
	t.Helper()
	var info unix.Sysinfo_t
	if err := unix.Sysinfo(&info); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile("/proc/vmstat")
	if err != nil {
		t.Fatal(err)
	}
	counters := map[string]uint64{}
	for line := range strings.SplitSeq(string(contents), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || (fields[0] != "pswpin" && fields[0] != "pswpout") {
			continue
		}
		pages, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		counters[fields[0]] = pages * uint64(os.Getpagesize())
	}
	if len(counters) != 2 {
		t.Fatalf("missing host swap counters: %v", counters)
	}
	return domain.SwapSnapshot{TotalBytes: uint64(info.Totalswap) * uint64(info.Unit), SwappedInBytes: counters["pswpin"], SwappedOutBytes: counters["pswpout"]}
}
