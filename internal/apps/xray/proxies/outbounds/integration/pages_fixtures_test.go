//go:build integration && linux

package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"net/url"
	"testing"
	"time"

	v1 "github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/grpcapi/proto/v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
)

type pageObservation struct {
	Outbounds     []record  `json:"outbounds"`
	NextPageToken string    `json:"next_page_token"`
	Generation    uint64    `json:"generation,string"`
	ObservedAt    time.Time `json:"observed_at"`
	DefaultState  string    `json:"default_state"`
	Error         string    `json:"error"`
}

func page(t *testing.T, c clients, transport, token string) pageObservation {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var data []byte
	switch transport {
	case "http":
		response, err := c.http.Get(c.address + "/v1/xray/proxies/instances/edge-a/outbounds?page_token=" + url.QueryEscape(token))
		if err != nil {
			t.Fatal(err)
		}
		data, err = io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
	case "grpc":
		response, err := v1.NewOutboundsServiceClient(c.grpc).ListOutbounds(ctx, &v1.ListOutboundsRequest{InstanceId: "edge-a", PageToken: token})
		if err != nil {
			data, _ := json.Marshal(map[string]string{"error": status.Convert(err).Message()})
			var result pageObservation
			json.Unmarshal(data, &result)
			return result
		}
		data, err = (protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}).Marshal(response)
		if err != nil {
			t.Fatal(err)
		}
	case "nats":
		input, _ := json.Marshal(map[string]any{"instance_id": "edge-a", "page_token": token})
		response, err := c.nats.RequestWithContext(ctx, "fixture.outbounds.xray.proxies.outbounds.v1.list", input)
		if err != nil {
			t.Fatal(err)
		}
		data = response.Data
	}
	var result pageObservation
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	return result
}
