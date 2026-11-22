//go:build integration && linux

package integration_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	v1 "github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/grpcapi/proto/v1"
	configsv1 "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/grpcapi/proto/v1"
	instancesv1 "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/grpcapi/proto/v1"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/proxy/blackhole"
	"github.com/xtls/xray-core/proxy/freedom"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type replacementPlan struct {
	ID                          string `json:"plan_id"`
	Mode                        string `json:"mode"`
	Reason                      string `json:"reason"`
	Generation                  uint64 `json:"generation,string"`
	DefaultState                string `json:"default_state"`
	ExistingConnectionsRetained bool   `json:"existing_connections_retained"`
	NativeResourcesMayRemain    bool   `json:"native_resources_may_remain"`
	Error                       string `json:"error"`
}
type replacementResult struct {
	ID                       string `json:"plan_id"`
	Outcome                  string `json:"outcome"`
	Error                    string `json:"error"`
	RequiresInstanceRestart  bool   `json:"requires_instance_restart"`
	NativeResourcesMayRemain bool   `json:"native_resources_may_remain"`
	Steps                    []struct {
		Kind         string `json:"kind"`
		Effect       string `json:"effect"`
		Attempted    bool   `json:"attempted"`
		Acknowledged bool   `json:"acknowledged"`
	} `json:"steps"`
}

func candidate(t *testing.T, tag, protocol string) []byte {
	t.Helper()
	config := &core.OutboundHandlerConfig{Tag: tag}
	switch protocol {
	case "freedom":
		config.ProxySettings = serial.ToTypedMessage(&freedom.Config{})
	case "blackhole":
		config.ProxySettings = serial.ToTypedMessage(&blackhole.Config{})
	default:
		config.ProxySettings = &serial.TypedMessage{Type: "xray.proxy.unregistered.Config"}
	}
	content, err := proto.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	return content
}
func replacementCall(t *testing.T, c clients, transport, operation string, input map[string]any) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if transport == "grpc" {
		client := v1.NewOutboundsServiceClient(c.grpc)
		var response proto.Message
		var err error
		if operation == "plan" {
			content, _ := input["configuration"].([]byte)
			tag, _ := input["tag"].(string)
			defaultPolicy, _ := input["default_policy"].(string)
			connections, _ := input["connection_policy"].(string)
			request := &v1.PlanOutboundReplacementRequest{InstanceId: "edge-a", Tag: tag, Configuration: content, DefaultPolicy: defaultPolicy, ConnectionPolicy: connections}
			if content, ok := input["source"].([]byte); ok {
				request.Sources = []*configsv1.ConfigurationSource{{Name: "candidate.json", Content: content}}
			}
			response, err = client.PlanOutboundReplacement(ctx, request)
		} else {
			request := &v1.OutboundReplacementRequest{InstanceId: "edge-a", PlanId: input["plan_id"].(string)}
			if operation == "apply" {
				response, err = client.ApplyOutboundReplacement(ctx, request)
			} else {
				response, err = client.GetOutboundReplacement(ctx, request)
			}
		}
		if err != nil {
			data, _ := json.Marshal(map[string]string{"error": status.Convert(err).Message()})
			return data
		}
		data, err := (protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}).Marshal(response)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	values := map[string]any{}
	for k, v := range input {
		values[k] = v
	}
	if content, ok := values["source"].([]byte); ok {
		delete(values, "source")
		values["sources"] = []any{map[string]any{"name": "candidate.json", "content": content}}
	}
	if transport == "nats" {
		values["instance_id"] = "edge-a"
		data, _ := json.Marshal(values)
		message, err := c.nats.RequestWithContext(ctx, "fixture.outbounds.xray.proxies.outbounds.replacements.v1."+operation, data)
		if err != nil {
			t.Fatal(err)
		}
		return message.Data
	}
	method, path := http.MethodPost, "replacement-plans"
	if operation == "apply" {
		path = "replacements"
	} else if operation == "get" {
		path = "replacements/" + url.PathEscape(values["plan_id"].(string))
		method = http.MethodGet
		values = nil
	}
	data, _ := json.Marshal(values)
	var body io.Reader = bytes.NewReader(data)
	if method == http.MethodGet {
		body = nil
	}
	request, _ := http.NewRequestWithContext(ctx, method, c.address+"/v1/xray/proxies/instances/edge-a/outbounds/"+path, body)
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("cacheable replacement evidence")
	}
	data, err = io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func planReplacement(t *testing.T, c clients, transport, tag string, content []byte, defaults, connections string) replacementPlan {
	t.Helper()
	data := replacementCall(t, c, transport, "plan", map[string]any{"tag": tag, "configuration": content, "default_policy": defaults, "connection_policy": connections})
	var p replacementPlan
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatal(err)
	}
	return p
}
func applyReplacement(t *testing.T, c clients, transport, id string) replacementResult {
	t.Helper()
	data := replacementCall(t, c, transport, "apply", map[string]any{"plan_id": id})
	var result replacementResult
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func tunnel(t *testing.T, proxy string, target *net.TCPListener) (net.Conn, net.Conn) {
	t.Helper()
	client, err := net.DialTimeout("tcp", proxy, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	client.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := fmt.Fprintf(client, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target.Addr(), target.Addr()); err != nil {
		t.Fatal(err)
	}
	target.SetDeadline(time.Now().Add(2 * time.Second))
	upstream, err := target.AcceptTCP()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { upstream.Close() })
	response, err := http.ReadResponse(bufio.NewReader(client), &http.Request{Method: "CONNECT"})
	if err != nil || response.StatusCode != 200 {
		t.Fatal(response, err)
	}
	return client, upstream
}
func tunnelPayload(t *testing.T, client, upstream net.Conn, marker string) {
	t.Helper()
	client.SetDeadline(time.Now().Add(time.Second))
	upstream.SetDeadline(time.Now().Add(time.Second))
	if _, err := io.WriteString(client, marker); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, len(marker))
	if _, err := io.ReadFull(upstream, data); err != nil || string(data) != marker {
		t.Fatal("established traffic changed", string(data), err)
	}
}
func tunnelTarget(t *testing.T) *net.TCPListener {
	t.Helper()
	target, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { target.Close() })
	return target
}

func restartExecution(t *testing.T, c clients, transport string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	switch transport {
	case "http":
		restartInstance(t, c)
	case "grpc":
		if _, err := instancesv1.NewInstancesServiceClient(c.grpc).RestartInstance(ctx, &instancesv1.RestartInstanceRequest{InstanceId: "edge-a"}); err != nil {
			t.Fatal(err)
		}
	case "nats":
		response, err := c.nats.RequestWithContext(ctx, "fixture.outbounds.core.engines.instances.v1.restart", []byte(`{"instance_id":"edge-a"}`))
		if err != nil || bytes.Contains(response.Data, []byte(`"error"`)) {
			t.Fatal(response, err)
		}
	}
}
