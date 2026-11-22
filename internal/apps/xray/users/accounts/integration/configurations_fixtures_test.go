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

	v1 "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/grpcapi/proto/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

type preparedAccounts struct {
	InstanceID string    `json:"instance_id"`
	Content    []byte    `json:"content"`
	SHA256     string    `json:"sha256"`
	PreparedAt time.Time `json:"prepared_at"`
	DurationNS string    `json:"duration_ns"`
}

func prepareAccounts(t *testing.T, c clients, transport string, source []byte, accounts [][]byte) preparedAccounts {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var data []byte
	if transport == "grpc" {
		result, err := v1.NewAccountsServiceClient(c.grpc).PrepareAccountConfiguration(ctx, &v1.PrepareAccountConfigurationRequest{InstanceId: "edge-a", Sources: []*v1.AccountSource{{Name: "base.json", Content: source}}, Inbounds: []*v1.InboundUserSet{{InboundTag: "members", Accounts: accounts}}})
		if err != nil {
			t.Fatal(err)
		}
		data, err = protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
	} else {
		if accounts == nil {
			accounts = [][]byte{}
		}
		input := map[string]any{"sources": []any{map[string]any{"name": "base.json", "content": source}}, "inbounds": []any{map[string]any{"inbound_tag": "members", "accounts": accounts}}}
		if transport == "nats" {
			input["instance_id"] = "edge-a"
		}
		body, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		if transport == "http" {
			request, _ := http.NewRequestWithContext(ctx, "POST", c.address+"/v1/xray/users/instances/edge-a/accounts/configurations", bytes.NewReader(body))
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
			response, err := c.nats.RequestWithContext(ctx, "fixture.accounts.xray.users.accounts.configurations.v1.prepare", body)
			if err != nil {
				t.Fatal(err)
			}
			data = response.Data
		}
	}
	var result preparedAccounts
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(string(data), err)
	}
	return result
}
