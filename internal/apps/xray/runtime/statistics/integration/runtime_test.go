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

	instancesv1 "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/grpcapi/proto/v1"
	statisticsv1 "github.com/PapilioVPN/node/internal/apps/xray/runtime/statistics/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/testutil"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
)

type observation struct {
	InstanceID              string    `json:"instance_id"`
	Generation              uint64    `json:"generation,string"`
	Goroutines              uint32    `json:"goroutines"`
	GCCycles                uint32    `json:"gc_cycles"`
	HeapAllocatedBytes      uint64    `json:"heap_allocated_bytes,string"`
	HeapTotalAllocatedBytes uint64    `json:"heap_total_allocated_bytes,string"`
	RuntimeSysBytes         uint64    `json:"runtime_sys_bytes,string"`
	Allocations             uint64    `json:"allocations,string"`
	Frees                   uint64    `json:"frees,string"`
	LiveObjects             uint64    `json:"live_objects,string"`
	GCPauseTotalNS          uint64    `json:"gc_pause_total_ns,string"`
	APIUptimeSeconds        uint32    `json:"api_uptime_seconds"`
	ObservedAt              time.Time `json:"observed_at"`
	DurationNS              int64     `json:"duration_ns,string"`
	Error                   string    `json:"error"`
}

func TestRuntimeStatisticsAcrossIndependentTransportsAndEngineRestart(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := brokerFixture(t, pki)
	for _, transport := range []string{"http", "grpc", "nats"} {
		t.Run(transport, func(t *testing.T) {
			f := newFixture(t, pki, broker, transport)
			client := startNode(t, f)
			awaitNative(t, f.native)
			awaitInstanceReady(t, client, transport)
			result := readStatistics(t, client, transport, "edge-a")
			assertNativeStatistics(t, result, 1)
			if missing := readStatistics(t, client, transport, "missing"); missing.Error != "not_found" || missing.InstanceID != "" {
				t.Fatalf("unknown instance returned facts %+v", missing)
			}
			controlInstance(t, client, transport, "stop")
			stopped := readStatistics(t, client, transport, "edge-a")
			if stopped.Error != "failed_precondition" || stopped.InstanceID != "" {
				t.Fatalf("stopped engine produced zero-success facts %+v", stopped)
			}
			controlInstance(t, client, transport, "start")
			awaitInstanceReady(t, client, transport)
			result = readStatistics(t, client, transport, "edge-a")
			assertNativeStatistics(t, result, 2)
		})
	}
}
func TestStatisticsNeedsExplicitAPIConfiguration(t *testing.T) {
	f := newFixture(t, testutil.NewPKI(t), "", "http")
	f.config.Instances[0].Xray.API = nil
	client := startNode(t, f)
	awaitNative(t, f.native)
	result := readStatistics(t, client, "http", "edge-a")
	if result.Error != "failed_precondition" || result.InstanceID != "" {
		t.Fatalf("unconfigured API was discovered implicitly %+v", result)
	}
}
func readStatistics(t *testing.T, c clients, transport, id string) observation {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var data []byte
	var err error
	switch transport {
	case "http":
		request, _ := http.NewRequestWithContext(ctx, "GET", c.address+"/v1/xray/runtime/instances/"+id+"/statistics", nil)
		response, callErr := c.http.Do(request)
		if callErr != nil {
			t.Fatal(callErr)
		}
		data, err = io.ReadAll(response.Body)
		response.Body.Close()
		if response.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("runtime facts can be cached")
		}
	case "grpc":
		result, callErr := statisticsv1.NewStatisticsServiceClient(c.grpc).GetRuntimeStatistics(ctx, &statisticsv1.GetRuntimeStatisticsRequest{InstanceId: id})
		if callErr != nil {
			return observation{Error: status.Convert(callErr).Message()}
		}
		data, err = protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}.Marshal(result)
	case "nats":
		payload, _ := json.Marshal(map[string]string{"instance_id": id})
		message, callErr := c.nats.RequestWithContext(ctx, "fixture.statistics.xray.runtime.statistics.v1.get", payload)
		if callErr != nil {
			t.Fatal(callErr)
		}
		data = message.Data
	}
	if err != nil {
		t.Fatal(err)
	}
	var result observation
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("invalid statistics JSON %s: %v", data, err)
	}
	return result
}
func assertNativeStatistics(t *testing.T, result observation, generation uint64) {
	t.Helper()
	if result.Error != "" || result.InstanceID != "edge-a" || result.Generation != generation || result.Goroutines == 0 || result.HeapAllocatedBytes == 0 || result.RuntimeSysBytes < result.HeapAllocatedBytes || result.HeapTotalAllocatedBytes < result.HeapAllocatedBytes || result.Allocations < result.Frees || result.Allocations-result.Frees != result.LiveObjects || result.ObservedAt.IsZero() || result.DurationNS < 0 {
		t.Fatalf("invalid native statistics %+v", result)
	}
}
func controlInstance(t *testing.T, c clients, transport, operation string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	switch transport {
	case "grpc":
		client := instancesv1.NewInstancesServiceClient(c.grpc)
		var err error
		if operation == "stop" {
			_, err = client.StopInstance(ctx, &instancesv1.StopInstanceRequest{InstanceId: "edge-a"})
		} else {
			_, err = client.StartInstance(ctx, &instancesv1.StartInstanceRequest{InstanceId: "edge-a"})
		}
		if err != nil {
			t.Fatal(err)
		}
	case "http":
		method := "PUT"
		if operation == "stop" {
			method = "DELETE"
		}
		request, _ := http.NewRequestWithContext(ctx, method, c.address+"/v1/core/engines/instances/edge-a/execution", nil)
		response, err := c.http.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatal("instance control status " + strconv.Itoa(response.StatusCode))
		}
	case "nats":
		response, err := c.nats.RequestWithContext(ctx, "fixture.statistics.core.engines.instances.v1."+operation, []byte(`{"instance_id":"edge-a"}`))
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(response.Data, &result) != nil || result.Error != "" {
			t.Fatalf("instance control failed %s", response.Data)
		}
	}
}

// This controller chain probes Node's configured API channel before reading facts.
// It also waits for the native channel's ordinary reconnect after engine restart.
func awaitInstanceReady(t *testing.T, c clients, transport string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		ready := false
		switch transport {
		case "grpc":
			result, err := instancesv1.NewInstancesServiceClient(c.grpc).CheckInstanceReadiness(ctx, &instancesv1.CheckInstanceReadinessRequest{InstanceId: "edge-a"})
			ready = err == nil && result.ApiState == "ready"
		case "http":
			request, _ := http.NewRequestWithContext(ctx, "GET", c.address+"/v1/core/engines/instances/edge-a/readiness", nil)
			response, err := c.http.Do(request)
			if err == nil {
				var result struct {
					APIState string `json:"api_state"`
				}
				decodeErr := json.NewDecoder(response.Body).Decode(&result)
				response.Body.Close()
				ready = decodeErr == nil && response.StatusCode == 200 && result.APIState == "ready"
			}
		case "nats":
			response, err := c.nats.RequestWithContext(ctx, "fixture.statistics.core.engines.instances.readiness.v1.get", []byte(`{"instance_id":"edge-a"}`))
			if err == nil {
				var result struct {
					APIState string `json:"api_state"`
				}
				ready = json.Unmarshal(response.Data, &result) == nil && result.APIState == "ready"
			}
		}
		if ready {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("Node's native API channel did not become ready")
		case <-ticker.C:
		}
	}
}
