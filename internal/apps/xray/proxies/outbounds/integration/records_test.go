//go:build integration && linux

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	v1 "github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/testutil"
	"github.com/xtls/xray-core/core"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestNativeOutboundReadback(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := brokerFixture(t, pki)
	for _, transport := range []string{"http", "grpc", "nats"} {
		t.Run(transport, func(t *testing.T) {
			f := newFixture(t, pki, broker, transport)
			c := startNode(t, f)
			awaitNative(t, f.native)
			result := readOutbounds(t, c, transport)
			if len(result.Outbounds) != 1 || result.Outbounds[0].Tag != "baseline" || result.Generation != 1 {
				t.Fatal(result)
			}
			var config core.OutboundHandlerConfig
			if err := proto.Unmarshal(result.Outbounds[0].Configuration, &config); err != nil || config.ProxySettings.Type != "xray.proxy.blackhole.Config" {
				t.Fatal(&config, err)
			}
		})
	}
}

type observation struct {
	Generation uint64 `json:"generation,string"`
	Outbounds  []struct {
		Tag           string
		Configuration []byte
	}
}

func readOutbounds(t *testing.T, c clients, transport string) observation {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var data []byte
	switch transport {
	case "http":
		response, err := c.http.Get(c.address + "/v1/xray/proxies/instances/edge-a/outbounds")
		if err != nil {
			t.Fatal(err)
		}
		data, _ = io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != 200 || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("read %d %s", response.StatusCode, data)
		}
	case "grpc":
		result, err := v1.NewOutboundsServiceClient(c.grpc).ListOutbounds(ctx, &v1.ListOutboundsRequest{InstanceId: "edge-a"})
		if err != nil {
			t.Fatal(err)
		}
		data, err = protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
	case "nats":
		response, err := c.nats.RequestWithContext(ctx, "fixture.outbounds.xray.proxies.outbounds.v1.list", []byte(`{"instance_id":"edge-a"}`))
		if err != nil {
			t.Fatal(err)
		}
		data = response.Data
	}
	var result observation
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("%s %v", data, err)
	}
	return result
}

func TestOutboundReadWithoutNativeAPIIsExplicit(t *testing.T) {
	f := newFixture(t, testutil.NewPKI(t), "", "http")
	f.config.Instances[0].Xray.API = nil
	c := startNode(t, f)
	awaitNative(t, f.native)
	response, err := c.http.Get(c.address + "/v1/xray/proxies/instances/edge-a/outbounds")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	if response.StatusCode != 409 || !bytes.Contains(data, []byte(`"error":"failed_precondition"`)) {
		t.Fatal(response.StatusCode, string(data))
	}
}
