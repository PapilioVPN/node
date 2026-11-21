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

	v1 "github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/grpcapi/proto/v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
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
		client := v1.NewInboundsServiceClient(c.grpc)
		var result *v1.InboundOperation
		var err error
		if kind == "add" {
			result, err = client.AddInbound(ctx, &v1.AddInboundRequest{InstanceId: "edge-a", Configuration: content})
		} else {
			result, err = client.RemoveInbound(ctx, &v1.RemoveInboundRequest{InstanceId: "edge-a", Tag: tag})
		}
		if err != nil {
			for _, detail := range status.Convert(err).Details() {
				if value, ok := detail.(*v1.InboundOperation); ok {
					return operation{value.Tag, value.Generation, value.Effect, value.Acknowledged, value.RequiresInstanceRestart}, status.Convert(err).Message()
				}
			}
			t.Fatal(err)
		}
		return operation{result.Tag, result.Generation, result.Effect, result.Acknowledged, result.RequiresInstanceRestart}, ""
	}
	if transport == "http" {
		method, path := http.MethodPost, c.address+"/v1/xray/proxies/instances/edge-a/inbounds"
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
		resp, err := c.nats.RequestWithContext(ctx, "fixture.inbounds.xray.proxies.inbounds.v1."+kind, body)
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
func nativeRecords(t *testing.T, c clients, transport string, tagsOnly bool) ([]record, []string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var data []byte
	var err error
	switch transport {
	case "grpc":
		client := v1.NewInboundsServiceClient(c.grpc)
		if tagsOnly {
			r, e := client.ListInboundTags(ctx, &v1.ListInboundTagsRequest{InstanceId: "edge-a"})
			if e != nil {
				t.Fatal(e)
			}
			return nil, r.Tags
		}
		r, e := client.ListInbounds(ctx, &v1.ListInboundsRequest{InstanceId: "edge-a"})
		if e != nil {
			t.Fatal(e)
		}
		data, err = protojson.MarshalOptions{UseProtoNames: true}.Marshal(r)
	case "http":
		path := "inbounds"
		if tagsOnly {
			path = "inbound-tags"
		}
		resp, e := c.http.Get(c.address + "/v1/xray/proxies/instances/edge-a/" + path)
		if e != nil {
			t.Fatal(e)
		}
		data, err = io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("%d %s", resp.StatusCode, data)
		}
	case "nats":
		suffix := "v1.list"
		if tagsOnly {
			suffix = "tags.v1.list"
		}
		resp, e := c.nats.RequestWithContext(ctx, "fixture.inbounds.xray.proxies.inbounds."+suffix, []byte(`{"instance_id":"edge-a"}`))
		if e != nil {
			t.Fatal(e)
		}
		data = resp.Data
	}
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Inbounds []record `json:"inbounds"`
		Tags     []string `json:"tags"`
		Error    string   `json:"error"`
	}
	if err := json.Unmarshal(data, &result); err != nil || result.Error != "" {
		t.Fatalf("%s %v", data, err)
	}
	return result.Inbounds, result.Tags
}
