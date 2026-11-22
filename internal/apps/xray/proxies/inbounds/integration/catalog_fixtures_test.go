//go:build integration && linux

package integration_test

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/domain"
	v1 "github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/infra/jsonwire"
	"google.golang.org/protobuf/encoding/protojson"
)

func catalogPage(t *testing.T, c clients, transport, kind, cursor string) []byte {
	t.Helper()
	if transport == "grpc" {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		client := v1.NewInboundsServiceClient(c.grpc)
		options := protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}
		switch kind {
		case "summaries":
			r, err := client.ListInboundSummaries(ctx, &v1.ListInboundSummariesRequest{InstanceId: "edge-a", Cursor: cursor})
			if err != nil {
				t.Fatal(err)
			}
			data, err := options.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			return data
		case "records":
			r, err := client.ListInbounds(ctx, &v1.ListInboundsRequest{InstanceId: "edge-a", Cursor: cursor})
			if err != nil {
				t.Fatal(err)
			}
			data, err := options.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			return data
		default:
			r, err := client.ListInboundTags(ctx, &v1.ListInboundTagsRequest{InstanceId: "edge-a", Cursor: cursor})
			if err != nil {
				t.Fatal(err)
			}
			data, err := options.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			return data
		}
	}
	input := map[string]any{"instance_id": "edge-a"}
	if cursor != "" {
		input["cursor"] = cursor
	}
	path, suffix := "inbound-summaries", "summaries.v1.list"
	if kind == "records" {
		path, suffix = "inbounds", "v1.list"
	}
	if kind == "tags" {
		path, suffix = "inbound-tags", "tags.v1.list"
	}
	path = "/v1/xray/proxies/instances/edge-a/" + path
	if cursor != "" {
		path += "?cursor=" + url.QueryEscape(cursor)
	} else {
		path += "?page_size=0"
	}
	return jsonRequest(t, c, transport, "GET", path, "xray.proxies.inbounds."+suffix, input)
}
func compatibility(t *testing.T, c clients, transport, tag string) domain.Compatibility {
	t.Helper()
	var data []byte
	if transport == "grpc" {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		r, err := v1.NewInboundsServiceClient(c.grpc).GetInboundCompatibility(ctx, &v1.GetInboundCompatibilityRequest{InstanceId: "edge-a", Tag: tag})
		if err != nil {
			t.Fatal(err)
		}
		data, err = protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
	} else {
		data = jsonRequest(t, c, transport, "GET", "/v1/xray/proxies/instances/edge-a/inbounds/compatibility?tag="+url.QueryEscape(tag), "xray.proxies.inbounds.compatibility.v1.get", map[string]any{"instance_id": "edge-a", "tag": tag})
	}
	var r struct {
		InstanceID string           `json:"instance_id"`
		Generation uint64           `json:"generation,string"`
		Inbound    jsonwire.Summary `json:"inbound"`
		Observed   time.Time        `json:"observed_at"`
		Error      string           `json:"error"`
	}
	if err := json.Unmarshal(data, &r); err != nil || r.Error != "" {
		t.Fatal(string(data), err)
	}
	return domain.Compatibility{InstanceID: r.InstanceID, Generation: r.Generation, Inbound: domain.Summary{Tag: r.Inbound.Tag, Direction: r.Inbound.Direction, Protocol: r.Inbound.Protocol, NativeType: r.Inbound.NativeType, AccountType: r.Inbound.AccountType, LiveAccounts: r.Inbound.LiveAccounts, Reason: r.Inbound.Reason, EmailMatching: r.Inbound.EmailMatching}, ObservedAt: r.Observed}
}
