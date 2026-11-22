//go:build integration && linux

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	v1 "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/grpcapi/proto/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

type configurationChange struct {
	Error       string `json:"error"`
	Persistence struct {
		Durable   bool `json:"durable"`
		Published bool `json:"published"`
		Metadata  struct {
			Revision uint64 `json:"revision,string"`
		} `json:"metadata"`
	} `json:"persistence"`
	Execution *struct {
		Effect string `json:"effect"`
		Status struct {
			Generation uint64 `json:"generation,string"`
		} `json:"status"`
	} `json:"execution"`
}

// This fixture calls the existing Configs API, retaining its independent source
// revision/persistence/restart contract instead of adding another writer here.
func replaceConfiguration(t *testing.T, c clients, transport string, expected uint64, content []byte) configurationChange {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	var data []byte
	if transport == "grpc" {
		result, err := v1.NewConfigurationsServiceClient(c.grpc).ReplaceConfiguration(ctx, &v1.ReplaceConfigurationRequest{InstanceId: "edge-a", ExpectedRevision: &expected, Sources: []*v1.ConfigurationSource{{Name: "replacement.json", Content: content}}})
		if err != nil {
			t.Fatal(err)
		}
		data, err = (protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}).Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
	} else {
		input := map[string]any{"expected_revision": fmt.Sprint(expected), "sources": []any{map[string]any{"name": "replacement.json", "content": content}}}
		if transport == "nats" {
			input["instance_id"] = "edge-a"
			body, _ := json.Marshal(input)
			message, err := c.nats.RequestWithContext(ctx, "fixture.outbounds.xray.runtime.configs.v1.replace", body)
			if err != nil {
				t.Fatal(err)
			}
			data = message.Data
		} else {
			body, _ := json.Marshal(input)
			request, _ := http.NewRequestWithContext(ctx, "PUT", c.address+"/v1/xray/runtime/instances/edge-a/configuration", bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			response, err := c.http.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			data, err = io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	var result configurationChange
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("Configs response %s: %v", data, err)
	}
	return result
}
