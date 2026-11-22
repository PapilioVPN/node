//go:build integration && linux

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	v1 "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/grpcapi/proto/v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type operation struct {
	InboundTag              string `json:"inbound_tag"`
	Email                   string `json:"email"`
	Generation              uint64 `json:"generation,string"`
	Effect                  string `json:"effect"`
	Acknowledged            bool   `json:"acknowledged"`
	RequiresInstanceRestart bool   `json:"requires_instance_restart"`
	Kind                    string `json:"kind"`
}

func accountMutation(t *testing.T, c clients, transport, kind, email string, content []byte) (operation, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var data []byte
	if transport == "grpc" {
		native := v1.NewAccountsServiceClient(c.grpc)
		var result *v1.AccountOperation
		var err error
		if kind == "add" {
			result, err = native.AddAccount(ctx, &v1.AddAccountRequest{InstanceId: "edge-a", InboundTag: "members", Configuration: content})
		} else {
			result, err = native.RemoveAccount(ctx, &v1.RemoveAccountRequest{InstanceId: "edge-a", InboundTag: "members", Email: proto.String(email)})
		}
		if err != nil {
			for _, detail := range status.Convert(err).Details() {
				if value, ok := detail.(*v1.AccountOperation); ok {
					return operation{value.InboundTag, value.Email, value.Generation, value.Effect, value.Acknowledged, value.RequiresInstanceRestart, value.Kind}, status.Convert(err).Message()
				}
			}
			t.Fatal(err)
		}
		return operation{result.InboundTag, result.Email, result.Generation, result.Effect, result.Acknowledged, result.RequiresInstanceRestart, result.Kind}, ""
	}
	if transport == "http" {
		method, path := http.MethodPost, c.address+"/v1/xray/users/instances/edge-a/accounts?inbound_tag=members"
		if kind == "remove" {
			method = http.MethodDelete
			path = c.address + "/v1/xray/users/instances/edge-a/account?inbound_tag=members&email=" + url.QueryEscape(email)
		}
		request, _ := http.NewRequestWithContext(ctx, method, path, bytes.NewReader(content))
		request.Header.Set("Content-Type", "application/x-protobuf")
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
			t.Fatal("private mutation response is cacheable")
		}
	} else {
		input := map[string]any{"instance_id": "edge-a", "inbound_tag": "members"}
		if kind == "add" {
			input["configuration"] = content
		} else {
			input["email"] = email
		}
		body, _ := json.Marshal(input)
		response, err := c.nats.RequestWithContext(ctx, "fixture.accounts.xray.users.accounts.v1."+kind, body)
		if err != nil {
			t.Fatal(err)
		}
		data = response.Data
	}
	var result struct {
		Operation operation
		Error     string
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("%s: %v", data, err)
	}
	return result.Operation, result.Error
}

type controlState struct {
	Generation              uint64     `json:"generation,string"`
	RequestInFlight         bool       `json:"request_in_flight"`
	RequiresInstanceRestart bool       `json:"requires_instance_restart"`
	LastOperation           *operation `json:"last_operation"`
}

func accountControl(t *testing.T, c clients, transport string) controlState {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var result controlState
	switch transport {
	case "http":
		response, err := c.http.Get(c.address + "/v1/xray/users/instances/edge-a/accounts/control-status")
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if err := json.NewDecoder(response.Body).Decode(&result); err != nil || response.StatusCode != 200 {
			t.Fatal(result, err, response.StatusCode)
		}
	case "grpc":
		state, err := v1.NewAccountsServiceClient(c.grpc).GetAccountControlStatus(ctx, &v1.GetAccountControlStatusRequest{InstanceId: "edge-a"})
		if err != nil {
			t.Fatal(err)
		}
		result = controlState{Generation: state.Generation, RequestInFlight: state.RequestInFlight, RequiresInstanceRestart: state.RequiresInstanceRestart}
		if value := state.LastOperation; value != nil {
			result.LastOperation = &operation{value.InboundTag, value.Email, value.Generation, value.Effect, value.Acknowledged, value.RequiresInstanceRestart, value.Kind}
		}
	case "nats":
		response, err := c.nats.RequestWithContext(ctx, "fixture.accounts.xray.users.accounts.status.v1.get", []byte(`{"instance_id":"edge-a"}`))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(response.Data, &result); err != nil {
			t.Fatal(err)
		}
	}
	return result
}
