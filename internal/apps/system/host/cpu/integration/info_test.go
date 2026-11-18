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

const infoSubject = "fixture.node-a.system.host.cpu.info.v1.get"

type processorInfo struct {
	ID               uint32   `json:"id"`
	VendorID         *string  `json:"vendor_id"`
	Family           *string  `json:"family"`
	Model            *string  `json:"model"`
	ModelName        *string  `json:"model_name"`
	Stepping         *string  `json:"stepping"`
	Microcode        *string  `json:"microcode"`
	PackageID        *int32   `json:"package_id"`
	CoreID           *int32   `json:"core_id"`
	ReportedCores    *uint32  `json:"reported_cores"`
	ReportedSiblings *uint32  `json:"reported_siblings"`
	ReportedMHz      *float64 `json:"reported_mhz"`
	CacheBytes       *uint64  `json:"cache_bytes,string"`
	Features         []string `json:"reported_features"`
	Bugs             []string `json:"reported_bugs"`
}

type processorInventory struct {
	LogicalCount      uint32          `json:"logical_count"`
	PackageCount      *uint32         `json:"package_count"`
	PhysicalCoreCount *uint32         `json:"physical_core_count"`
	Processors        []processorInfo `json:"processors"`
	ObservedAt        time.Time       `json:"observed_at"`
	DurationNS        int64           `json:"duration_ns,string"`
}

func assertProcessorInfo(t *testing.T, client clients) {
	t.Helper()
	expected := hostCPUInfo(t)
	if client.HTTP != nil {
		started := time.Now()
		response, err := client.HTTP.Get(strings.TrimSuffix(client.HTTPAddress, "/load") + "/cpu/info")
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("HTTP inventory: %s %v", body, err)
		}
		var result processorInventory
		if err := json.Unmarshal(body, &result); err != nil {
			t.Fatalf("inventory JSON: %s %v", body, err)
		}
		assertInventory(t, result, expected, started)
	}
	if client.GRPC != nil {
		started := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		result, err := cpuv1.NewHostCPUServiceClient(client.GRPC).GetHostCPUInfo(ctx, &cpuv1.GetHostCPUInfoRequest{})
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		if result.ObservedAt.CheckValid() != nil {
			t.Fatal("invalid inventory timestamp")
		}
		got := processorInventory{LogicalCount: result.LogicalCount, PackageCount: result.PackageCount, PhysicalCoreCount: result.PhysicalCoreCount, ObservedAt: result.ObservedAt.AsTime(), DurationNS: result.DurationNs}
		for _, row := range result.Processors {
			got.Processors = append(got.Processors, processorInfo{ID: row.Id, VendorID: row.VendorId, Family: row.Family, Model: row.Model, ModelName: row.ModelName, Stepping: row.Stepping, Microcode: row.Microcode, PackageID: row.PackageId, CoreID: row.CoreId, ReportedCores: row.ReportedCores, ReportedSiblings: row.ReportedSiblings, ReportedMHz: row.ReportedMhz, CacheBytes: row.CacheBytes, Features: row.ReportedFeatures, Bugs: row.ReportedBugs})
		}
		assertInventory(t, got, expected, started)
		ctx, cancel = context.WithCancel(context.Background())
		cancel()
		if _, err := cpuv1.NewHostCPUServiceClient(client.GRPC).GetHostCPUInfo(ctx, &cpuv1.GetHostCPUInfoRequest{}); status.Code(err) != codes.Canceled {
			t.Fatalf("canceled inventory: %v", err)
		}
	}
	if client.NATS != nil {
		started := time.Now()
		response, err := client.NATS.Request(infoSubject, []byte("{}"), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		var result processorInventory
		if err := json.Unmarshal(response.Data, &result); err != nil {
			t.Fatalf("inventory JSON: %s %v", response.Data, err)
		}
		assertInventory(t, result, expected, started)
		expired := nats.NewMsg(infoSubject)
		expired.Data = []byte("{}")
		expired.Header.Set("Papilio-Deadline", time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano))
		resultExpired, err := client.NATS.RequestMsg(expired, time.Second)
		if err != nil || string(resultExpired.Data) != `{"error":"deadline_exceeded"}` {
			t.Fatalf("expired inventory: %v %v", resultExpired, err)
		}
	}
}

func assertInventory(t *testing.T, got processorInventory, expected map[uint32]map[string]string, started time.Time) {
	t.Helper()
	if got.LogicalCount != uint32(len(expected)) || len(got.Processors) != len(expected) || len(expected) == 0 {
		t.Fatalf("incomplete CPU inventory: %d/%d", got.LogicalCount, len(expected))
	}
	packages := map[int32]uint32{}
	seen := map[uint32]bool{}
	topologyKnown := true
	for _, row := range got.Processors {
		raw, exists := expected[row.ID]
		if !exists || seen[row.ID] {
			t.Fatalf("unknown/duplicate processor: %d", row.ID)
		}
		seen[row.ID] = true
		for _, field := range []struct {
			key string
			got *string
		}{{"vendor_id", row.VendorID}, {"cpu family", row.Family}, {"model", row.Model}, {"model name", row.ModelName}, {"stepping", row.Stepping}, {"microcode", row.Microcode}} {
			want, reported := raw[field.key]
			if reported && (field.got == nil || *field.got != want) || !reported && field.got != nil {
				t.Fatalf("CPU %d field %s: %v; want %q", row.ID, field.key, field.got, want)
			}
		}
		for _, field := range []struct {
			key string
			got *int32
		}{{"physical id", row.PackageID}, {"core id", row.CoreID}} {
			value, ok := raw[field.key]
			if !ok || value == "-1" {
				if field.got != nil {
					t.Fatalf("invented topology: %d %s", row.ID, field.key)
				}
				continue
			}
			number, err := strconv.ParseInt(value, 10, 32)
			if err != nil || field.got == nil || int64(*field.got) != number {
				t.Fatalf("CPU %d topology %s: %q %v", row.ID, field.key, value, err)
			}
		}
		if row.PackageID == nil || row.ReportedCores == nil {
			topologyKnown = false
		} else {
			packages[*row.PackageID] = *row.ReportedCores
		}
		for _, field := range []struct {
			key string
			got *uint32
		}{{"cpu cores", row.ReportedCores}, {"siblings", row.ReportedSiblings}} {
			value, reported := raw[field.key]
			if !reported {
				if field.got != nil {
					t.Fatal("invented processor count")
				}
				continue
			}
			count, err := strconv.ParseUint(value, 10, 32)
			if err != nil || field.got == nil || uint64(*field.got) != count {
				t.Fatalf("CPU %d count %s: %q %v", row.ID, field.key, value, err)
			}
		}
		if value, reported := raw["cache size"]; reported {
			fields := strings.Fields(value)
			if len(fields) != 2 {
				t.Fatal("invalid reference cache unit")
			}
			kib, err := strconv.ParseUint(fields[0], 10, 64)
			if err != nil || row.CacheBytes == nil || *row.CacheBytes != kib*1024 {
				t.Fatalf("CPU %d cache bytes: %q %v", row.ID, value, err)
			}
		} else if row.CacheBytes != nil {
			t.Fatal("invented cache size")
		}
		if _, reported := raw["cpu MHz"]; reported {
			if row.ReportedMHz == nil || math.IsNaN(*row.ReportedMHz) || math.IsInf(*row.ReportedMHz, 0) || *row.ReportedMHz < 0 {
				t.Fatalf("invalid reported frequency: %+v", row)
			}
		}
		if strings.Join(row.Features, " ") != strings.Join(strings.Fields(raw["flags"]), " ") || strings.Join(row.Bugs, " ") != strings.Join(strings.Fields(raw["bugs"]), " ") {
			t.Fatalf("CPU %d lost reported features/bugs", row.ID)
		}
	}
	if topologyKnown {
		count := uint32(0)
		for _, cores := range packages {
			count += cores
		}
		if got.PackageCount == nil || *got.PackageCount != uint32(len(packages)) || got.PhysicalCoreCount == nil || *got.PhysicalCoreCount != count {
			t.Fatalf("incorrect reported package/core counts: %+v", got)
		}
	}
	if got.ObservedAt.Before(started) || got.ObservedAt.After(time.Now()) || got.DurationNS < 0 || got.DurationNS > int64(time.Since(started)) {
		t.Fatalf("invalid inventory timing: %+v", got)
	}
}
