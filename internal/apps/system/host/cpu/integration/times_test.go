//go:build integration && linux

package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/domain"
	cpuv1 "github.com/PapilioVPN/node/internal/apps/system/host/cpu/grpcapi/proto/v1"
	"github.com/nats-io/nats.go"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const timesSubject = "fixture.node-a.system.host.cpu.times.v1.get"

func assertFreshTimes(t *testing.T, client clients) {
	t.Helper()
	rate := hostTickRate(t)
	for _, perCPU := range []bool{false, true} {
		if client.HTTP != nil {
			before := readStat(t, perCPU)
			started := time.Now()
			url := strings.TrimSuffix(client.HTTPAddress, "/load") + "/cpu/times?per_cpu=" + strconv.FormatBool(perCPU)
			response, err := client.HTTP.Get(url)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil || response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "no-store" {
				t.Fatalf("HTTP CPU ticks: %s %v", body, err)
			}
			assertTimesJSON(t, body, started, perCPU, rate, before)
		}
		if client.GRPC != nil {
			before := readStat(t, perCPU)
			started := time.Now()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			result, err := cpuv1.NewHostCPUServiceClient(client.GRPC).GetHostCPUTimes(ctx, &cpuv1.GetHostCPUTimesRequest{PerCpu: perCPU})
			cancel()
			if err != nil {
				t.Fatal(err)
			}
			if result.ObservedAt.CheckValid() != nil {
				t.Fatal("invalid CPU observation timestamp")
			}
			rows := make([]domain.Ticks, 0, len(result.Counters))
			for _, row := range result.Counters {
				rows = append(rows, domain.Ticks{CPU: row.Cpu, User: row.UserTicks, Nice: row.NiceTicks, System: row.SystemTicks, Idle: row.IdleTicks, IOWait: row.IowaitTicks, IRQ: row.IrqTicks, SoftIRQ: row.SoftirqTicks, Steal: row.StealTicks, Guest: row.GuestTicks, GuestNice: row.GuestNiceTicks})
			}
			assertTimes(t, domain.Times{PerCPU: result.PerCpu, TicksPerSecond: result.TicksPerSecond, Counters: rows}, result.ObservedAt.AsTime(), time.Duration(result.DurationNs), started, perCPU, rate, before)
		}
		if client.NATS != nil {
			before := readStat(t, perCPU)
			started := time.Now()
			response, err := client.NATS.Request(timesSubject, []byte(`{"per_cpu":`+strconv.FormatBool(perCPU)+`}`), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			assertTimesJSON(t, response.Data, started, perCPU, rate, before)
		}
	}
	if client.NATS != nil {
		expired := nats.NewMsg(timesSubject)
		expired.Data = []byte("{}")
		expired.Header.Set("Papilio-Deadline", time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano))
		response, err := client.NATS.RequestMsg(expired, time.Second)
		if err != nil || string(response.Data) != `{"error":"deadline_exceeded"}` {
			t.Fatalf("expired CPU ticks: %v %v", response, err)
		}
	}
	if client.GRPC != nil {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := cpuv1.NewHostCPUServiceClient(client.GRPC).GetHostCPUTimes(ctx, &cpuv1.GetHostCPUTimesRequest{}); status.Code(err) != codes.Canceled {
			t.Fatalf("canceled CPU ticks: %v", err)
		}
	}
}

func assertTimesJSON(t *testing.T, body []byte, started time.Time, perCPU bool, rate uint64, before map[string][10]uint64) {
	t.Helper()
	var result struct {
		PerCPU   bool   `json:"per_cpu"`
		Rate     uint64 `json:"ticks_per_second,string"`
		Counters []struct {
			CPU       string `json:"cpu"`
			User      uint64 `json:"user_ticks,string"`
			Nice      uint64 `json:"nice_ticks,string"`
			System    uint64 `json:"system_ticks,string"`
			Idle      uint64 `json:"idle_ticks,string"`
			IOWait    uint64 `json:"iowait_ticks,string"`
			IRQ       uint64 `json:"irq_ticks,string"`
			SoftIRQ   uint64 `json:"softirq_ticks,string"`
			Steal     uint64 `json:"steal_ticks,string"`
			Guest     uint64 `json:"guest_ticks,string"`
			GuestNice uint64 `json:"guest_nice_ticks,string"`
		} `json:"counters"`
		ObservedAt time.Time `json:"observed_at"`
		DurationNS int64     `json:"duration_ns,string"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("invalid times JSON: %s %v", body, err)
	}
	rows := make([]domain.Ticks, 0, len(result.Counters))
	for _, row := range result.Counters {
		rows = append(rows, domain.Ticks{CPU: row.CPU, User: row.User, Nice: row.Nice, System: row.System, Idle: row.Idle, IOWait: row.IOWait, IRQ: row.IRQ, SoftIRQ: row.SoftIRQ, Steal: row.Steal, Guest: row.Guest, GuestNice: row.GuestNice})
	}
	assertTimes(t, domain.Times{PerCPU: result.PerCPU, TicksPerSecond: result.Rate, Counters: rows}, result.ObservedAt, time.Duration(result.DurationNS), started, perCPU, rate, before)
}

func assertTimes(t *testing.T, got domain.Times, observed time.Time, duration time.Duration, started time.Time, perCPU bool, rate uint64, before map[string][10]uint64) {
	t.Helper()
	after := readStat(t, perCPU)
	if got.PerCPU != perCPU || got.TicksPerSecond != rate || len(got.Counters) != len(before) || len(got.Counters) != len(after) || len(got.Counters) == 0 {
		t.Fatalf("lost selection, CPUs or units: %+v", got)
	}
	seen := make(map[string]bool, len(got.Counters))
	for _, row := range got.Counters {
		lower, haveBefore := before[row.CPU]
		upper, haveAfter := after[row.CPU]
		if !haveBefore || !haveAfter || seen[row.CPU] {
			t.Fatalf("lost/duplicate CPU identity: %s", row.CPU)
		}
		seen[row.CPU] = true
		for i, value := range [10]uint64{row.User, row.Nice, row.System, row.Idle, row.IOWait, row.IRQ, row.SoftIRQ, row.Steal, row.Guest, row.GuestNice} {
			if value < min(lower[i], upper[i]) || value > max(lower[i], upper[i]) {
				t.Fatalf("CPU %s counter %d: %d outside [%d,%d]", row.CPU, i, value, lower[i], upper[i])
			}
		}
	}
	if observed.Before(started) || observed.After(time.Now()) || duration < 0 || duration > time.Since(started) {
		t.Fatalf("invalid CPU observation timing: %v %v", observed, duration)
	}
}

// Parse outside-Node raw kernel counters independently of the production SDK.
func readStat(t *testing.T, perCPU bool) map[string][10]uint64 {
	t.Helper()
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		t.Fatal(err)
	}
	result := make(map[string][10]uint64)
	for line := range strings.SplitSeq(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || !strings.HasPrefix(fields[0], "cpu") {
			break
		}
		aggregate := fields[0] == "cpu"
		if aggregate == perCPU {
			continue
		}
		if len(fields) < 8 {
			t.Fatalf("short host CPU row: %s", line)
		}
		var values [10]uint64
		for i := 0; i < len(values) && i+1 < len(fields); i++ {
			values[i], err = strconv.ParseUint(fields[i+1], 10, 64)
			if err != nil {
				t.Fatal(err)
			}
		}
		name := fields[0]
		if aggregate {
			name = "cpu-total"
		}
		result[name] = values
	}
	return result
}
