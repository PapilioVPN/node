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

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	v1 "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/grpcapi/proto/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type batchResponse struct {
	Operation operation
	Items     []struct {
		Index     int
		Attempted bool
		Operation operation
		Error     string
	}
	Error string
}

func batchAccounts(t *testing.T, c clients, transport string, additions []domain.Addition, removals []domain.Removal, parallelism int) batchResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	kind, resource := "add", "additions"
	var items []map[string]any
	for _, item := range additions {
		items = append(items, map[string]any{"inbound_tag": item.InboundTag, "configuration": item.Configuration})
	}
	if removals != nil {
		kind, resource = "remove", "removals"
		for _, item := range removals {
			items = append(items, map[string]any{"inbound_tag": item.InboundTag, "email": item.Email})
		}
	}
	var data []byte
	if transport == "grpc" {
		client := v1.NewAccountsServiceClient(c.grpc)
		var result *v1.AccountBatch
		var err error
		if removals != nil {
			request := &v1.RemoveAccountsRequest{InstanceId: "edge-a", Parallelism: uint32(parallelism)}
			for _, item := range removals {
				request.Items = append(request.Items, &v1.AccountRemoval{InboundTag: item.InboundTag, Email: proto.String(item.Email)})
			}
			result, err = client.RemoveAccounts(ctx, request)
		} else {
			request := &v1.AddAccountsRequest{InstanceId: "edge-a", Parallelism: uint32(parallelism)}
			for _, item := range additions {
				request.Items = append(request.Items, &v1.AccountAddition{InboundTag: item.InboundTag, Configuration: item.Configuration})
			}
			result, err = client.AddAccounts(ctx, request)
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err = protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
	} else {
		input := map[string]any{"items": items, "parallelism": parallelism}
		if transport == "nats" {
			input["instance_id"] = "edge-a"
		}
		body, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		if transport == "http" {
			request, _ := http.NewRequestWithContext(ctx, "POST", c.address+"/v1/xray/users/instances/edge-a/accounts/"+resource, bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			response, err := c.http.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			data, err = io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil || response.StatusCode != 200 || response.Header.Get("Cache-Control") != "no-store" {
				t.Fatal(response.StatusCode, string(data), err)
			}
		} else {
			response, err := c.nats.RequestWithContext(ctx, "fixture.accounts.xray.users.accounts.batches.v1."+kind, body)
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
