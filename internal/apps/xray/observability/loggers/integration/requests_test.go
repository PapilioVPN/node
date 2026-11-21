//go:build integration && linux

package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	loggersv1 "github.com/PapilioVPN/node/internal/apps/xray/observability/loggers/grpcapi/proto/v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
)

type operation struct {
	InstanceID              string     `json:"instance_id"`
	Generation              uint64     `json:"generation,string"`
	Effect                  string     `json:"effect"`
	Acknowledged            bool       `json:"acknowledged"`
	RequiresInstanceRestart bool       `json:"requires_instance_restart"`
	ObservedAt              *time.Time `json:"observed_at"`
	DurationNS              int64      `json:"duration_ns,string"`
}
type controlStatus struct {
	InstanceID              string     `json:"instance_id"`
	Generation              uint64     `json:"generation,string"`
	ExecutionState          string     `json:"execution_state"`
	APIConfigured           bool       `json:"api_configured"`
	RequestInFlight         bool       `json:"request_in_flight"`
	RequiresInstanceRestart bool       `json:"requires_instance_restart"`
	LastOperation           *operation `json:"last_operation"`
}

func reopen(t *testing.T, c clients, transport string) (operation, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if transport == "grpc" {
		result, err := loggersv1.NewLoggersServiceClient(c.grpc).ReopenLogger(ctx, &loggersv1.ReopenLoggerRequest{InstanceId: "edge-a"})
		if err != nil {
			for _, detail := range status.Convert(err).Details() {
				if value, ok := detail.(*loggersv1.LoggerOperation); ok {
					return protoOperation(t, value), status.Convert(err).Message()
				}
			}
			return operation{}, status.Convert(err).Message()
		}
		return protoOperation(t, result), ""
	}
	var data []byte
	if transport == "http" {
		request, _ := http.NewRequestWithContext(ctx, "POST", c.address+"/v1/xray/observability/instances/edge-a/logger/reopenings", nil)
		response, err := c.http.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		data, err = io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if response.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("private control response can be cached")
		}
	} else {
		response, err := c.nats.RequestWithContext(ctx, "fixture.loggers.xray.observability.loggers.v1.reopen", []byte(`{"instance_id":"edge-a"}`))
		if err != nil {
			t.Fatal(err)
		}
		data = response.Data
	}
	var result struct {
		Operation operation `json:"operation"`
		Error     string    `json:"error"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("invalid control response %s: %v", data, err)
	}
	return result.Operation, result.Error
}
func protoOperation(t *testing.T, value *loggersv1.LoggerOperation) operation {
	t.Helper()
	data, err := protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var result operation
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	return result
}
func loggerStatus(t *testing.T, c clients, transport string) controlStatus {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var data []byte
	var err error
	switch transport {
	case "grpc":
		result, callErr := loggersv1.NewLoggersServiceClient(c.grpc).GetLoggerControlStatus(ctx, &loggersv1.GetLoggerControlStatusRequest{InstanceId: "edge-a"})
		if callErr != nil {
			t.Fatal(callErr)
		}
		data, err = protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}.Marshal(result)
	case "http":
		request, _ := http.NewRequestWithContext(ctx, "GET", c.address+"/v1/xray/observability/instances/edge-a/logger/control-status", nil)
		response, callErr := c.http.Do(request)
		if callErr != nil {
			t.Fatal(callErr)
		}
		data, err = io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatalf("control status %d %s", response.StatusCode, data)
		}
	case "nats":
		response, callErr := c.nats.RequestWithContext(ctx, "fixture.loggers.xray.observability.loggers.status.v1.get", []byte(`{"instance_id":"edge-a"}`))
		if callErr != nil {
			t.Fatal(callErr)
		}
		data = response.Data
	}
	if err != nil {
		t.Fatal(err)
	}
	var result controlStatus
	if err := json.Unmarshal(data, &result); err != nil || result.InstanceID != "edge-a" {
		t.Fatalf("invalid control readback %s: %v", data, err)
	}
	return result
}
