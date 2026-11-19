//go:build integration && linux

package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"testing"
	"time"

	runtimev1 "github.com/PapilioVPN/node/internal/apps/system/node/runtime/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/testutil"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestRuntimeOverIndependentTransports(t *testing.T) {
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
			assertRuntimeReads(t, startConfiguredNode(t, pki, config), config, int32(os.Getpid()))
		})
	}
}
func assertRuntimeReads(t *testing.T, client clients, config settings.Config, pid int32) {
	t.Helper()
	check := func(operation string, data []byte) {
		t.Helper()
		switch operation {
		case "info":
			var result struct {
				PID           int32
				GoVersion     string    `json:"go_version"`
				InitializedAt time.Time `json:"initialized_at"`
				ObservedAt    time.Time `json:"observed_at"`
			}
			if err := json.Unmarshal(data, &result); err != nil || result.PID != pid || result.GoVersion == "" || result.InitializedAt.IsZero() || result.ObservedAt.IsZero() {
				t.Fatalf("invalid info %s %v", data, err)
			}
		case "resources":
			var result struct {
				PID        int32
				TickRate   uint64 `json:"ticks_per_second,string"`
				Goroutines uint64 `json:"goroutines,string"`
				Mapped     uint64 `json:"runtime_mapped_bytes,string"`
				Heap       uint64 `json:"heap_objects_bytes,string"`
			}
			if err := json.Unmarshal(data, &result); err != nil || result.PID != pid || result.TickRate == 0 || result.Goroutines == 0 || result.Mapped < result.Heap {
				t.Fatalf("invalid process resources %s %v", data, err)
			}
		case "diagnostics":
			var result struct {
				Healthy bool
				HTTP    *struct{ Ready bool }
				GRPC    *struct{ Ready bool }
				NATS    *struct {
					Succeeded bool   `json:"probe_succeeded"`
					RoundTrip *int64 `json:"round_trip_ns,string"`
				}
			}
			if err := json.Unmarshal(data, &result); err != nil || !result.Healthy || (result.HTTP != nil) != (config.Communications.HTTP != nil) || (result.GRPC != nil) != (config.Communications.GRPC != nil) || (result.NATS != nil) != (config.Communications.NATS != nil) {
				t.Fatalf("invalid component presence %s %v", data, err)
			}
			if result.HTTP != nil && !result.HTTP.Ready || result.GRPC != nil && !result.GRPC.Ready || result.NATS != nil && (!result.NATS.Succeeded || result.NATS.RoundTrip == nil) {
				t.Fatalf("unhealthy native component: %s", data)
			}
		}
	}
	if client.HTTP != nil {
		for _, operation := range []string{"info", "resources", "diagnostics"} {
			path := client.HTTPAddress
			if operation != "info" {
				path += "/" + operation
			}
			response, err := client.HTTP.Get(path)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil || response.StatusCode != 200 {
				t.Fatalf("HTTP %d %s %v", response.StatusCode, body, err)
			}
			check(operation, body)
		}
	}
	if client.GRPC != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		api := runtimev1.NewNodeRuntimeServiceClient(client.GRPC)
		info, err := api.GetNodeInfo(ctx, &runtimev1.GetNodeInfoRequest{})
		if err != nil {
			t.Fatal(err)
		}
		resources, err := api.GetNodeResources(ctx, &runtimev1.GetNodeResourcesRequest{})
		if err != nil {
			t.Fatal(err)
		}
		diagnostics, err := api.GetNodeDiagnostics(ctx, &runtimev1.GetNodeDiagnosticsRequest{})
		if err != nil {
			t.Fatal(err)
		}
		for _, operation := range []struct {
			name    string
			message proto.Message
		}{{"info", info}, {"resources", resources}, {"diagnostics", diagnostics}} {
			body, err := (protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}).Marshal(operation.message)
			if err != nil {
				t.Fatal(err)
			}
			check(operation.name, body)
		}
	}
	if client.NATS != nil {
		for _, operation := range []string{"info", "resources", "diagnostics"} {
			message, err := client.NATS.Request("fixture.node-a.system.node.runtime."+operation+".v1.get", []byte("{}"), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			check(operation, message.Data)
		}
	}
}
