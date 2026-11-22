//go:build integration && linux

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	v1 "github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/grpcapi/proto/v1"
	instancesv1 "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/grpcapi/proto/v1"
	stats "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type trafficPage struct {
	Counters []struct {
		Name  string `json:"name"`
		Bytes int64  `json:"bytes,string"`
	} `json:"counters"`
	Generation    uint64    `json:"generation,string"`
	ObservedAt    time.Time `json:"observed_at"`
	Source        string    `json:"source"`
	NextPageToken string    `json:"next_page_token"`
	Error         string    `json:"error"`
}
type trafficPair struct {
	Uplink, Downlink *observation
	Error            string `json:"error"`
}
type resetResult struct {
	Epoch            string `json:"epoch"`
	OperationID      string `json:"operation_id"`
	Revision         uint64 `json:"revision,string"`
	Generation       uint64 `json:"generation,string"`
	Outcome          string `json:"outcome"`
	Error            string `json:"error"`
	RecoveryRequired bool   `json:"recovery_required"`
	Total            uint32 `json:"total"`
	NextOffset       uint32 `json:"next_offset"`
	Items            []struct {
		Name          string `json:"name"`
		Attempted     bool   `json:"attempted"`
		Effect        string `json:"effect"`
		PreviousBytes *int64 `json:"previous_bytes,string"`
	} `json:"items"`
}

func invokeDecode[T any](t *testing.T, f *fixture, transport, operation string, input map[string]any) T {
	t.Helper()
	data := f.invoke(t, transport, operation, input)
	var result T
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("%s %s decode %s: %v", transport, operation, data, err)
	}
	return result
}
func (f *fixture) invoke(t *testing.T, transport, operation string, input map[string]any) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()
	values := make(map[string]any, len(input)+1)
	for k, v := range input {
		values[k] = v
	}
	values["instance_id"] = "edge-a"
	if transport == "grpc" {
		stringValue := func(key string) string {
			if v, ok := values[key].(string); ok {
				return v
			}
			return ""
		}
		number := func(key string) uint32 {
			switch v := values[key].(type) {
			case int:
				return uint32(v)
			case uint32:
				return v
			}
			return 0
		}
		var response proto.Message
		var err error
		scope := map[string]v1.Scope{"user": v1.Scope_SCOPE_USER, "inbound": v1.Scope_SCOPE_INBOUND, "outbound": v1.Scope_SCOPE_OUTBOUND}[stringValue("scope")]
		switch operation {
		case "list":
			response, err = f.grpc.ListTrafficCounters(ctx, &v1.ListTrafficCountersRequest{InstanceId: "edge-a", Scope: scope, PageToken: stringValue("page_token"), PageSize: number("page_size")})
		case "pair":
			response, err = f.grpc.GetTrafficPair(ctx, &v1.GetTrafficPairRequest{InstanceId: "edge-a", Scope: scope, Name: stringValue("name")})
		case "reset_status":
			var revision uint64
			fmt.Sscan(stringValue("revision"), &revision)
			response, err = f.grpc.GetTrafficResetStatus(ctx, &v1.GetTrafficResetStatusRequest{InstanceId: "edge-a", Revision: revision, Offset: number("offset")})
		default:
			var revision uint64
			fmt.Sscan(stringValue("expected_revision"), &revision)
			request := &v1.ResetTrafficRequest{InstanceId: "edge-a", Epoch: stringValue("epoch"), OperationId: stringValue("operation_id"), ExpectedRevision: &revision, Scope: scope, Name: stringValue("name"), MaxCounters: number("max_counters"), Direction: map[string]v1.Direction{"uplink": v1.Direction_DIRECTION_UPLINK, "downlink": v1.Direction_DIRECTION_DOWNLINK}[stringValue("direction")]}
			switch operation {
			case "reset_counter":
				response, err = f.grpc.ResetTrafficCounter(ctx, request)
			case "reset_pair":
				response, err = f.grpc.ResetTrafficPair(ctx, request)
			case "reset_scope":
				response, err = f.grpc.ResetTrafficCounters(ctx, request)
			default:
				t.Fatal(operation)
			}
		}
		if err != nil {
			data, _ := json.Marshal(map[string]string{"error": status.Convert(err).Message()})
			return data
		}
		data, err := (protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}).Marshal(response)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	if transport == "nats" {
		data, _ := json.Marshal(values)
		message, err := f.nats.RequestWithContext(ctx, "fixture.traffic.xray.observability.traffic.v1."+operation, data)
		if err != nil {
			t.Fatal(err)
		}
		return message.Data
	}
	path := map[string]string{"list": "counters", "pair": "pair", "reset_counter": "counter-resets", "reset_pair": "pair-resets", "reset_scope": "scope-resets", "reset_status": "reset-status"}[operation]
	target := f.address + "/v1/xray/observability/instances/edge-a/traffic/" + path
	var request *http.Request
	var err error
	if operation == "reset_counter" || operation == "reset_pair" || operation == "reset_scope" {
		delete(values, "instance_id")
		data, _ := json.Marshal(values)
		request, err = http.NewRequestWithContext(ctx, "POST", target, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
	} else {
		query := url.Values{}
		for key, value := range input {
			query.Set(key, fmt.Sprint(value))
		}
		request, err = http.NewRequestWithContext(ctx, "GET", target+"?"+query.Encode(), nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	response, err := f.http.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("response cacheable")
	}
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func resetInput(status resetResult, scope, name, op string) map[string]any {
	result := map[string]any{"epoch": status.Epoch, "operation_id": op, "expected_revision": fmt.Sprint(status.Revision), "scope": scope}
	if name != "" {
		result["name"] = name
	}
	return result
}

func (f *fixture) restart(t *testing.T, transport string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	switch transport {
	case "http":
		request, _ := http.NewRequestWithContext(ctx, "POST", f.address+"/v1/core/engines/instances/edge-a/executions", nil)
		response, err := f.http.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			data, _ := io.ReadAll(response.Body)
			t.Fatal(response.StatusCode, string(data))
		}
	case "grpc":
		response, err := instancesv1.NewInstancesServiceClient(f.connection).RestartInstance(ctx, &instancesv1.RestartInstanceRequest{InstanceId: "edge-a"})
		if err != nil || response.GetStatus().GetGeneration() < 2 {
			t.Fatal(response, err)
		}
	case "nats":
		message, err := f.nats.RequestWithContext(ctx, "fixture.traffic.core.engines.instances.v1.restart", []byte(`{"instance_id":"edge-a"}`))
		if err != nil || bytes.Contains(message.Data, []byte(`"error"`)) {
			t.Fatal(message, err)
		}
	}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		_, err := f.api.GetSysStats(ctx, &stats.SysStatsRequest{})
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("replaced native API unavailable", err)
		case <-ticker.C:
		}
	}
}
