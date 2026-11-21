//go:build integration && linux

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	configsv1 "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/testutil"
)

func TestNativeCandidateValidationOverIndependentTransports(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := brokerFixture(t, pki)
	for _, transport := range []string{"http", "grpc", "nats"} {
		t.Run(transport, func(t *testing.T) {
			f := nodeFixture(t, pki, broker, transport)
			client := startNode(t, f)
			awaitTraffic(t, f)
			exerciseValidation(t, client, transport, f)
		})
	}
}

func exerciseValidation(t *testing.T, client clients, transport string, f fixture) {
	t.Helper()
	log := filepath.Join(f.config.Instances[0].Xray.WorkDirectory, "must-not-exist.log")
	candidate, _ := json.Marshal(map[string]any{"log": map[string]any{"access": log, "error": log}, "outbounds": []any{map[string]any{"protocol": "freedom"}}})
	for _, test := range []struct {
		format  string
		content []byte
		valid   bool
	}{{"json", candidate, true}, {"json", []byte(`{"inbounds":[{"protocol":"not-a-protocol"}]}`), false}, {"protobuf", nil, true}} {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		var valid bool
		switch transport {
		case "http":
			request, _ := http.NewRequestWithContext(ctx, "POST", client.address+"/v1/xray/runtime/instances/edge-a/config-validations?format="+test.format, bytes.NewReader(test.content))
			response, err := client.http.Do(request)
			if err != nil {
				cancel()
				t.Fatal(err)
			}
			data, err := io.ReadAll(response.Body)
			response.Body.Close()
			var got struct {
				InstanceID string    `json:"instance_id"`
				Valid      bool      `json:"valid"`
				CheckedAt  time.Time `json:"checked_at"`
				DurationNS string    `json:"duration_ns"`
			}
			if err != nil || response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "no-store" || json.Unmarshal(data, &got) != nil || got.InstanceID != "edge-a" || got.CheckedAt.IsZero() || got.DurationNS == "" {
				cancel()
				t.Fatalf("HTTP %d %s %v", response.StatusCode, data, err)
			}
			valid = got.Valid
		case "grpc":
			result, err := configsv1.NewConfigurationsServiceClient(client.grpc).ValidateConfiguration(ctx, &configsv1.ValidateConfigurationRequest{InstanceId: "edge-a", Format: test.format, Content: test.content})
			if err != nil {
				cancel()
				t.Fatal(err)
			}
			if result.InstanceId != "edge-a" || result.CheckedAt == nil || result.CheckedAt.CheckValid() != nil || result.DurationNs < 0 {
				cancel()
				t.Fatalf("gRPC observation: %v", result)
			}
			valid = result.Valid
		case "nats":
			request, _ := json.Marshal(map[string]any{"instance_id": "edge-a", "format": test.format, "content": test.content})
			message, err := client.nats.RequestWithContext(ctx, subject, request)
			if err != nil {
				cancel()
				t.Fatal(err)
			}
			var result struct {
				InstanceID string `json:"instance_id"`
				Valid      bool   `json:"valid"`
				Error      string `json:"error"`
			}
			if json.Unmarshal(message.Data, &result) != nil || result.Error != "" || result.InstanceID != "edge-a" {
				cancel()
				t.Fatalf("NATS %s", message.Data)
			}
			valid = result.Valid
		}
		cancel()
		if valid != test.valid {
			t.Fatalf("accepted=%v wanted=%v", valid, test.valid)
		}
		if err := traffic(f.proxy, f.target.URL); err != nil {
			t.Fatalf("candidate check interrupted traffic: %v", err)
		}
	}
	current, err := os.ReadFile(filepath.Join(f.directory, "active.json"))
	if err != nil || !bytes.Equal(f.original, current) {
		t.Fatal("active configuration changed")
	}
	entries, err := os.ReadDir(f.config.Instances[0].Xray.WorkDirectory)
	if err != nil || len(entries) != 0 {
		t.Fatalf("private candidates or runtime logs leaked: %v %v", entries, err)
	}
}
