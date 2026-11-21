//go:build integration && linux

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	configsv1 "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/grpcapi/proto/v1"
	instancesv1 "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/grpcapi/proto/v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type stateMetadata struct {
	Revision    string `json:"revision"`
	SHA256      string `json:"sha256"`
	Durable     bool   `json:"durable"`
	SourceCount int    `json:"source_count"`
}
type stateResponse struct {
	Metadata stateMetadata `json:"metadata"`
	Sources  []struct {
		Name    string `json:"name"`
		Content []byte `json:"content"`
	} `json:"sources"`
	Persistence struct {
		Metadata           stateMetadata `json:"metadata"`
		Published          bool          `json:"published"`
		PublicationUnknown bool          `json:"publication_unknown"`
		Durable            bool          `json:"durable"`
	} `json:"persistence"`
	Execution *struct {
		Effect string `json:"effect"`
		Status *struct {
			State      string `json:"state"`
			Generation string `json:"generation"`
			Revision   string `json:"configuration_revision"`
		} `json:"status"`
	} `json:"execution"`
	Error string `json:"error"`
}

func stateCall(t *testing.T, c clients, transport, operation string, revision uint64, sources []domain.Source) stateResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var data []byte
	var err error
	if transport == "grpc" {
		client := configsv1.NewConfigurationsServiceClient(c.grpc)
		var message proto.Message
		switch operation {
		case "get":
			message, err = client.GetConfiguration(ctx, &configsv1.GetConfigurationRequest{InstanceId: "edge-a"})
		case "status":
			message, err = client.GetConfigurationStatus(ctx, &configsv1.GetConfigurationRequest{InstanceId: "edge-a"})
		case "replace":
			request := &configsv1.ReplaceConfigurationRequest{InstanceId: "edge-a", ExpectedRevision: &revision}
			for _, s := range sources {
				request.Sources = append(request.Sources, &configsv1.ConfigurationSource{Name: s.Name, Content: s.Content})
			}
			message, err = client.ReplaceConfiguration(ctx, request)
		case "apply":
			message, err = client.ApplyConfiguration(ctx, &configsv1.ApplyConfigurationRequest{InstanceId: "edge-a", ExpectedRevision: &revision})
		default:
			t.Fatal("unknown test operation")
		}
		if err != nil {
			var response stateResponse
			response.Error = status.Convert(err).Message()
			for _, detail := range status.Convert(err).Details() {
				if change, ok := detail.(*configsv1.ConfigurationChange); ok {
					encoded, marshalErr := protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}.Marshal(change)
					if marshalErr != nil {
						t.Fatal(marshalErr)
					}
					if json.Unmarshal(encoded, &response) != nil {
						t.Fatal("invalid change detail")
					}
				}
			}
			return response
		}
		data, err = protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}.Marshal(message)
	} else {
		payload := map[string]any{}
		if transport == "nats" {
			payload["instance_id"] = "edge-a"
		}
		if operation == "replace" || operation == "apply" {
			payload["expected_revision"] = strconv.FormatUint(revision, 10)
		}
		if operation == "replace" {
			wire := make([]map[string]any, len(sources))
			for i, s := range sources {
				wire[i] = map[string]any{"name": s.Name, "content": s.Content}
			}
			payload["sources"] = wire
		}
		body, _ := json.Marshal(payload)
		if transport == "http" {
			method, path := "GET", "/v1/xray/runtime/instances/edge-a/configuration"
			switch operation {
			case "status":
				path += "/status"
			case "replace":
				method = "PUT"
			case "apply":
				method = "POST"
				path += "/applications"
			}
			request, _ := http.NewRequestWithContext(ctx, method, c.address+path, bytes.NewReader(body))
			response, callErr := c.http.Do(request)
			if callErr != nil {
				t.Fatal(callErr)
			}
			data, err = io.ReadAll(response.Body)
			response.Body.Close()
			if response.Header.Get("Cache-Control") != "no-store" {
				t.Fatal("private source response can be cached")
			}
		} else {
			suffix := map[string]string{"get": "v1.get", "status": "status.v1.get", "replace": "v1.replace", "apply": "v1.apply"}[operation]
			message, callErr := c.nats.RequestWithContext(ctx, "fixture.configs.xray.runtime.configs."+suffix, body)
			if callErr != nil {
				t.Fatal(callErr)
			}
			data = message.Data
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	var result stateResponse
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("invalid state response %s: %v", data, err)
	}
	return result
}

func awaitProxy(t *testing.T, f fixture) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		connection, err := net.DialTimeout("tcp", f.proxy, 50*time.Millisecond)
		if err == nil {
			connection.Close()
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("restored Xray listener unavailable")
		case <-ticker.C:
		}
	}
}

func assertExecutionRevision(t *testing.T, c clients, transport, revision, generation string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var data []byte
	var err error
	switch transport {
	case "grpc":
		result, callErr := instancesv1.NewInstancesServiceClient(c.grpc).GetInstanceStatus(ctx, &instancesv1.GetInstanceStatusRequest{InstanceId: "edge-a"})
		if callErr != nil {
			t.Fatal(callErr)
		}
		data, err = protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}.Marshal(result)
	case "http":
		request, _ := http.NewRequestWithContext(ctx, "GET", c.address+"/v1/core/engines/instances/edge-a", nil)
		response, callErr := c.http.Do(request)
		if callErr != nil {
			t.Fatal(callErr)
		}
		data, err = io.ReadAll(response.Body)
		response.Body.Close()
	case "nats":
		message, callErr := c.nats.RequestWithContext(ctx, "fixture.configs.core.engines.instances.status.v1.get", []byte(`{"instance_id":"edge-a"}`))
		if callErr != nil {
			t.Fatal(callErr)
		}
		data = message.Data
	}
	if err != nil {
		t.Fatal(err)
	}
	var observation struct {
		State      string `json:"state"`
		Revision   string `json:"configuration_revision"`
		Generation string `json:"generation"`
	}
	if err := json.Unmarshal(data, &observation); err != nil || observation.State != "running" || observation.Revision != revision || observation.Generation != generation {
		t.Fatalf("managed execution status=%s: %v", data, err)
	}
}
