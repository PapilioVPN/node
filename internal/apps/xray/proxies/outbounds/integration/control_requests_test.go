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

	v1 "github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/grpcapi/proto/v1"
	"google.golang.org/grpc/status"
)

type operation struct {
	Tag                     string `json:"tag"`
	Generation              uint64 `json:"generation,string"`
	Effect                  string `json:"effect"`
	Acknowledged            bool   `json:"acknowledged"`
	RequiresInstanceRestart bool   `json:"requires_instance_restart"`
}
type record struct {
	Tag           string `json:"tag"`
	Configuration []byte `json:"configuration"`
}

func nativeMutation(t *testing.T, c clients, transport, kind, tag string, content []byte) (operation, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var data []byte
	if transport == "grpc" {
		client := v1.NewOutboundsServiceClient(c.grpc)
		var result *v1.OutboundOperation
		var err error
		if kind == "add" {
			result, err = client.AddOutbound(ctx, &v1.AddOutboundRequest{InstanceId: "edge-a", Configuration: content})
		} else {
			result, err = client.RemoveOutbound(ctx, &v1.RemoveOutboundRequest{InstanceId: "edge-a", Tag: tag})
		}
		if err != nil {
			for _, detail := range status.Convert(err).Details() {
				if value, ok := detail.(*v1.OutboundOperation); ok {
					return operation{value.Tag, value.Generation, value.Effect, value.Acknowledged, value.RequiresInstanceRestart}, status.Convert(err).Message()
				}
			}
			t.Fatal(err)
		}
		return operation{result.Tag, result.Generation, result.Effect, result.Acknowledged, result.RequiresInstanceRestart}, ""
	}
	if transport == "http" {
		method, path := http.MethodPost, c.address+"/v1/xray/proxies/instances/edge-a/outbounds"
		if kind == "remove" {
			method = http.MethodDelete
			path += "?tag=" + url.QueryEscape(tag)
		}
		req, _ := http.NewRequestWithContext(ctx, method, path, bytes.NewReader(content))
		req.Header.Set("Content-Type", "application/x-protobuf")
		resp, err := c.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, err = io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if resp.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("private response cacheable")
		}
	} else {
		input := map[string]any{"instance_id": "edge-a"}
		if kind == "add" {
			input["configuration"] = content
		} else {
			input["tag"] = tag
		}
		body, _ := json.Marshal(input)
		resp, err := c.nats.RequestWithContext(ctx, "fixture.outbounds.xray.proxies.outbounds.v1."+kind, body)
		if err != nil {
			t.Fatal(err)
		}
		data = resp.Data
	}
	var result struct {
		Operation operation `json:"operation"`
		Error     string    `json:"error"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("%s: %v", data, err)
	}
	return result.Operation, result.Error
}
