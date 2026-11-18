//go:build integration && linux

package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	processesv1 "github.com/PapilioVPN/node/internal/apps/system/host/processes/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/testutil"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestProcessObservationsOverIndependentlyEnabledTransports(t *testing.T) {
	pki := testutil.NewPKI(t)
	all := nodeConfig(t, pki)
	pid := startProcess(t)
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
			assertProcessReads(t, startConfiguredNode(t, pki, config), pid)
		})
	}
}

func startProcess(t *testing.T) int32 {
	t.Helper()
	command := exec.Command("sleep", "60")
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait() })
	return int32(command.Process.Pid)
}

func assertProcessReads(t *testing.T, client clients, pid int32) {
	t.Helper()
	checkRead := func(data []byte) {
		t.Helper()
		var result struct {
			Process struct {
				PID       int32
				ParentPID int32 `json:"parent_pid"`
				Name      string
				Start     uint64 `json:"start_ticks,string"`
				RSS       uint64 `json:"resident_bytes,string"`
			}
			Rate       uint64    `json:"ticks_per_second,string"`
			ObservedAt time.Time `json:"observed_at"`
			Duration   int64     `json:"duration_ns,string"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatal(err)
		}
		if result.Process.PID != pid || result.Process.ParentPID != int32(os.Getpid()) || result.Process.Name != "sleep" || result.Process.Start == 0 || result.Rate == 0 || result.Duration < 0 || result.ObservedAt.IsZero() || result.ObservedAt.After(time.Now()) {
			t.Fatalf("wrong native process: %s", data)
		}
	}
	checkCount := func(data []byte) {
		t.Helper()
		var result struct {
			Count uint64 `json:"count,string"`
		}
		if err := json.Unmarshal(data, &result); err != nil || result.Count == 0 {
			t.Fatalf("invalid count: %s %v", data, err)
		}
	}
	checkPage := func(data []byte) {
		t.Helper()
		var result struct {
			Entries []struct {
				PID     int32
				Process *struct{ PID int32 }
				Issue   string
			}
			Next *int32 `json:"next_after_pid"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatal(err)
		}
		if len(result.Entries) != 1 || result.Entries[0].PID != pid || result.Entries[0].Process == nil || result.Entries[0].Process.PID != pid || result.Entries[0].Issue != "" || result.Next != nil && *result.Next != pid {
			t.Fatalf("wrong selected page: %s", data)
		}
	}
	if client.HTTP != nil {
		for _, operation := range []struct {
			suffix string
			check  func([]byte)
		}{{"/count", checkCount}, {"/" + strconv.Itoa(int(pid)), checkRead}, {fmt.Sprintf("?after_pid=%d&limit=1", pid-1), checkPage}} {
			response, err := client.HTTP.Get(client.HTTPAddress + operation.suffix)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil || response.StatusCode != 200 {
				t.Fatalf("HTTP: %d %s %v", response.StatusCode, body, err)
			}
			operation.check(body)
		}
	}
	if client.GRPC != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		api := processesv1.NewHostProcessesServiceClient(client.GRPC)
		count, err := api.CountProcesses(ctx, &processesv1.CountProcessesRequest{})
		if err != nil {
			t.Fatal(err)
		}
		read, err := api.GetProcess(ctx, &processesv1.GetProcessRequest{Pid: pid})
		if err != nil {
			t.Fatal(err)
		}
		page, err := api.ListProcesses(ctx, &processesv1.ListProcessesRequest{AfterPid: pid - 1, Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		options := protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}
		body, err := options.Marshal(count)
		if err != nil {
			t.Fatal(err)
		}
		checkCount(body)
		body, err = options.Marshal(read)
		if err != nil {
			t.Fatal(err)
		}
		checkRead(body)
		body, err = options.Marshal(page)
		if err != nil {
			t.Fatal(err)
		}
		checkPage(body)
	}
	if client.NATS != nil {
		for _, operation := range []struct {
			suffix, input string
			check         func([]byte)
		}{{"count.v1.get", `{}`, checkCount}, {"v1.get", fmt.Sprintf(`{"pid":%d}`, pid), checkRead}, {"v1.list", fmt.Sprintf(`{"after_pid":%d,"limit":1}`, pid-1), checkPage}} {
			message, err := client.NATS.Request("fixture.node-a.system.host.processes."+operation.suffix, []byte(operation.input), 2*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			operation.check(message.Data)
		}
	}
}
