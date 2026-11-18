//go:build integration && linux

package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	cpuv1 "github.com/PapilioVPN/node/internal/apps/system/host/cpu/grpcapi/proto/v1"
	"github.com/nats-io/nats.go"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
)

const activitySubject = "fixture.node-a.system.host.cpu.activity.v1.get"

type activityObservation struct {
	ContextSwitches uint64    `json:"context_switches,string"`
	Interrupts      uint64    `json:"interrupts,string"`
	SoftInterrupts  uint64    `json:"soft_interrupts,string"`
	CreatedTasks    uint64    `json:"created_tasks,string"`
	RunnableTasks   uint64    `json:"runnable_tasks,string"`
	IOBlockedTasks  uint64    `json:"io_blocked_tasks,string"`
	ObservedAt      time.Time `json:"observed_at"`
	DurationNS      int64     `json:"duration_ns,string"`
}

func assertActivityReads(t *testing.T, client clients) {
	t.Helper()
	check := func(body []byte, before map[string]uint64, started time.Time) {
		t.Helper()
		after := hostActivity(t)
		var got activityObservation
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("activity JSON: %s %v", body, err)
		}
		for key, value := range map[string]uint64{"ctxt": got.ContextSwitches, "intr": got.Interrupts, "softirq": got.SoftInterrupts, "processes": got.CreatedTasks} {
			if value < before[key] || value > after[key] {
				t.Fatalf("%s=%d outside native observations %d/%d", key, value, before[key], after[key])
			}
		}
		// Runnable/blocked gauges may fluctuate between samples; exact field mapping
		// and known zero values are checked with controlled boundary fixtures.
		if got.ObservedAt.Before(started) || got.ObservedAt.After(time.Now()) || got.DurationNS < 0 || got.DurationNS > int64(time.Since(started)) {
			t.Fatalf("invalid activity timing: %+v", got)
		}
	}
	if client.HTTP != nil {
		before, started := hostActivity(t), time.Now()
		response, err := client.HTTP.Get(strings.TrimSuffix(client.HTTPAddress, "/load") + "/cpu/activity")
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("activity HTTP: %s %v", body, err)
		}
		check(body, before, started)
	}
	if client.GRPC != nil {
		before, started := hostActivity(t), time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		result, err := cpuv1.NewHostCPUServiceClient(client.GRPC).GetHostCPUActivity(ctx, &cpuv1.GetHostCPUActivityRequest{})
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		body, err := protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		check(body, before, started)
		ctx, cancel = context.WithCancel(context.Background())
		cancel()
		if _, err := cpuv1.NewHostCPUServiceClient(client.GRPC).GetHostCPUActivity(ctx, &cpuv1.GetHostCPUActivityRequest{}); status.Code(err) != codes.Canceled {
			t.Fatalf("canceled activity: %v", err)
		}
	}
	if client.NATS != nil {
		before, started := hostActivity(t), time.Now()
		response, err := client.NATS.Request(activitySubject, []byte("{}"), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		check(response.Data, before, started)
		message := nats.NewMsg(activitySubject)
		message.Data = []byte("{}")
		message.Header.Set("Papilio-Deadline", time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano))
		response, err = client.NATS.RequestMsg(message, time.Second)
		if err != nil || string(response.Data) != `{"error":"deadline_exceeded"}` {
			t.Fatalf("expired activity: %v %v", response, err)
		}
	}
}
