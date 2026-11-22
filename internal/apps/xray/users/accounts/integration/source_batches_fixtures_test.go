//go:build integration && linux

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	configs "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	v1 "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/grpcapi/proto/v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

// Native message packages for SS2022 and WireGuard also link their complete
// server implementations. Inspect only the pinned string fields for fidelity.
func nativeStringField(t *testing.T, data []byte, field protowire.Number) []string {
	t.Helper()
	var values []string
	for len(data) > 0 {
		number, kind, n := protowire.ConsumeTag(data)
		if n < 0 {
			t.Fatal("invalid native field")
		}
		data = data[n:]
		size := protowire.ConsumeFieldValue(number, kind, data)
		if size < 0 {
			t.Fatal("invalid native value")
		}
		if number == field {
			if kind != protowire.BytesType {
				t.Fatal("invalid native string field")
			}
			value, n := protowire.ConsumeString(data[:size])
			if n < 0 {
				t.Fatal("invalid native string")
			}
			values = append(values, value)
		}
		data = data[size:]
	}
	return values
}

func sourceBatchAccounts(t *testing.T, c clients, transport string, selections []domain.SourceSelection, sources []configs.Source, parallelism int) batchResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	var data []byte
	if transport == "grpc" {
		request := &v1.AddAccountsRequest{InstanceId: "edge-a", Parallelism: uint32(parallelism)}
		for _, item := range selections {
			request.Items = append(request.Items, &v1.AccountAddition{InboundTag: item.InboundTag, Email: proto.String(item.Email)})
		}
		for _, source := range sources {
			request.Sources = append(request.Sources, &v1.AccountSource{Name: source.Name, Content: source.Content})
		}
		result, err := v1.NewAccountsServiceClient(c.grpc).AddAccounts(ctx, request)
		if err != nil {
			if len(selections) == 0 {
				return batchResponse{Error: status.Convert(err).Message()}
			}
			t.Fatal(err)
		}
		data, err = protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
	} else {
		items := make([]map[string]any, 0, len(selections))
		for _, item := range selections {
			items = append(items, map[string]any{"inbound_tag": item.InboundTag, "email": item.Email})
		}
		files := make([]map[string]any, 0, len(sources))
		for _, source := range sources {
			files = append(files, map[string]any{"name": source.Name, "content": source.Content})
		}
		input := map[string]any{"items": items, "sources": files, "parallelism": parallelism}
		if transport == "nats" {
			input["instance_id"] = "edge-a"
		}
		body, _ := json.Marshal(input)
		if transport == "http" {
			request, _ := http.NewRequestWithContext(ctx, "POST", c.address+"/v1/xray/users/instances/edge-a/accounts/additions", bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			response, err := c.http.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			data, err = io.ReadAll(response.Body)
			response.Body.Close()
			wantStatus := 200
			if len(selections) == 0 {
				wantStatus = 400
			}
			if err != nil || response.StatusCode != wantStatus || response.Header.Get("Cache-Control") != "no-store" {
				t.Fatal(response.StatusCode, string(data), err)
			}
		} else {
			response, err := c.nats.RequestWithContext(ctx, "fixture.accounts.xray.users.accounts.batches.v1.add", body)
			if err != nil {
				t.Fatal(err)
			}
			data = response.Data
		}
	}
	var result batchResponse
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(string(data), err)
	}
	return result
}
