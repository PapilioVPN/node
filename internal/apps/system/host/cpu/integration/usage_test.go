//go:build integration && linux

package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	cpuv1 "github.com/PapilioVPN/node/internal/apps/system/host/cpu/grpcapi/proto/v1"
	"github.com/nats-io/nats.go"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const usageSubject = "fixture.node-a.system.host.cpu.usage.v1.get"

type sampledUsage struct {
	PerCPU              bool         `json:"per_cpu"`
	TicksPerSecond      uint64       `json:"ticks_per_second,string"`
	CPUs                []sampledCPU `json:"cpus"`
	RequestedIntervalNS int64        `json:"requested_interval_ns,string"`
	StartedAt           time.Time    `json:"started_at"`
	SampleIntervalNS    int64        `json:"sample_interval_ns,string"`
	ObservedAt          time.Time    `json:"observed_at"`
	DurationNS          int64        `json:"duration_ns,string"`
}

type sampledCPU struct {
	CPU        string   `json:"cpu"`
	State      string   `json:"state"`
	Percent    *float64 `json:"percent"`
	BusyTicks  *uint64  `json:"busy_ticks,string"`
	TotalTicks *uint64  `json:"total_ticks,string"`
}

func assertSampledUsage(t *testing.T, client clients) {
	t.Helper()
	const interval = 10 * time.Millisecond
	for _, perCPU := range []bool{false, true} {
		if client.HTTP != nil {
			before := readStat(t, perCPU)
			started := time.Now()
			response, err := client.HTTP.Get(strings.TrimSuffix(client.HTTPAddress, "/load") + "/cpu/usage?per_cpu=" + strconv.FormatBool(perCPU) + "&interval_ns=" + strconv.FormatInt(int64(interval), 10))
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil || response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "no-store" {
				t.Fatalf("HTTP sample: %d %s %v", response.StatusCode, body, err)
			}
			var result sampledUsage
			if err := json.Unmarshal(body, &result); err != nil {
				t.Fatalf("sample JSON: %s %v", body, err)
			}
			assertSample(t, result, started, perCPU, interval, before)
		}
		if client.GRPC != nil {
			before := readStat(t, perCPU)
			started := time.Now()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			requested := int64(interval)
			result, err := cpuv1.NewHostCPUServiceClient(client.GRPC).GetHostCPUUsage(ctx, &cpuv1.GetHostCPUUsageRequest{PerCpu: perCPU, IntervalNs: &requested})
			cancel()
			if err != nil {
				t.Fatal(err)
			}
			if result.StartedAt.CheckValid() != nil || result.ObservedAt.CheckValid() != nil {
				t.Fatal("invalid sample timestamps")
			}
			got := sampledUsage{PerCPU: result.PerCpu, TicksPerSecond: result.TicksPerSecond, RequestedIntervalNS: result.RequestedIntervalNs, StartedAt: result.StartedAt.AsTime(), SampleIntervalNS: result.SampleIntervalNs, ObservedAt: result.ObservedAt.AsTime(), DurationNS: result.DurationNs}
			for _, row := range result.Cpus {
				got.CPUs = append(got.CPUs, sampledCPU{CPU: row.Cpu, State: strings.ToLower(strings.TrimPrefix(row.State.String(), "CPU_USAGE_STATE_")), Percent: row.Percent, BusyTicks: row.BusyTicks, TotalTicks: row.TotalTicks})
			}
			assertSample(t, got, started, perCPU, interval, before)
		}
		if client.NATS != nil {
			before := readStat(t, perCPU)
			started := time.Now()
			response, err := client.NATS.Request(usageSubject, []byte(`{"per_cpu":`+strconv.FormatBool(perCPU)+`,"interval_ns":"`+strconv.FormatInt(int64(interval), 10)+`"}`), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			var result sampledUsage
			if err := json.Unmarshal(response.Data, &result); err != nil {
				t.Fatalf("sample JSON: %s %v", response.Data, err)
			}
			assertSample(t, result, started, perCPU, interval, before)
		}
	}
	if client.NATS != nil {
		request := nats.NewMsg(usageSubject)
		request.Data = []byte(`{"interval_ns":"1000000000"}`)
		request.Header.Set("Papilio-Deadline", time.Now().Add(20*time.Millisecond).UTC().Format(time.RFC3339Nano))
		result, err := client.NATS.RequestMsg(request, time.Second)
		if err != nil || string(result.Data) != `{"error":"deadline_exceeded"}` {
			t.Fatalf("NATS sample deadline: %v %v", result, err)
		}
	}
	if client.GRPC != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		requested := int64(time.Second)
		result, err := cpuv1.NewHostCPUServiceClient(client.GRPC).GetHostCPUUsage(ctx, &cpuv1.GetHostCPUUsageRequest{IntervalNs: &requested})
		if result != nil || status.Code(err) != codes.DeadlineExceeded {
			t.Fatalf("gRPC sample deadline: %v %v", result, err)
		}
	}
}

func assertSample(t *testing.T, got sampledUsage, started time.Time, perCPU bool, interval time.Duration, before map[string][10]uint64) {
	t.Helper()
	after := readStat(t, perCPU)
	if got.PerCPU != perCPU || got.TicksPerSecond != hostTickRate(t) || got.RequestedIntervalNS != int64(interval) || len(got.CPUs) != len(before) || len(got.CPUs) != len(after) || len(got.CPUs) == 0 {
		t.Fatalf("lost sample selection/CPUs: %+v", got)
	}
	if got.StartedAt.Before(started) || got.ObservedAt.Before(got.StartedAt) || got.ObservedAt.After(time.Now()) || got.SampleIntervalNS < int64(interval) || got.DurationNS < got.SampleIntervalNS || got.DurationNS > int64(time.Since(started)) {
		t.Fatalf("invalid sample timing: %+v", got)
	}
	seen := make(map[string]bool, len(got.CPUs))
	for _, row := range got.CPUs {
		lower, haveBefore := before[row.CPU]
		upper, haveAfter := after[row.CPU]
		if !haveBefore || !haveAfter || seen[row.CPU] {
			t.Fatalf("unknown/duplicate CPU: %+v", row)
		}
		seen[row.CPU] = true
		switch row.State {
		case "measured":
			if row.Percent == nil || row.BusyTicks == nil || row.TotalTicks == nil || *row.TotalTicks == 0 || *row.BusyTicks > *row.TotalTicks || math.IsNaN(*row.Percent) || math.IsInf(*row.Percent, 0) || math.Abs(*row.Percent-float64(*row.BusyTicks)/float64(*row.TotalTicks)*100) > 1e-9 {
				t.Fatalf("invalid measured CPU usage: %+v", row)
			}
			// Actual native deltas cannot exceed the outside-Node observation window.
			// I/O wait may decrease; do not invent a monotonic bound for such a window.
			var maximum uint64
			monotonic := true
			for i := 0; i < 8; i++ {
				if upper[i] < lower[i] {
					monotonic = false
					break
				}
				maximum += upper[i] - lower[i]
			}
			if monotonic && *row.TotalTicks > maximum {
				t.Fatalf("CPU %s returned %d ticks outside host window %d", row.CPU, *row.TotalTicks, maximum)
			}
		case "no_progress", "counter_decreased", "counter_overflow", "added", "removed":
			if row.Percent != nil || row.BusyTicks != nil || row.TotalTicks != nil {
				t.Fatalf("unknown window contains fabricated counters: %+v", row)
			}
		default:
			t.Fatalf("unknown CPU measurement state: %+v", row)
		}
	}
}
