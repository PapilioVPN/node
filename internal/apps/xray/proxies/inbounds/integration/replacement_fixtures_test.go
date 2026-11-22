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
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/domain"
	v1 "github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/infra/jsonwire"
	configsv1 "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/grpcapi/proto/v1"
	"github.com/xtls/xray-core/app/proxyman"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	xhttp "github.com/xtls/xray-core/proxy/http"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func jsonRequest(t *testing.T, c clients, transport, method, path, suffix string, input any) []byte {
	t.Helper()
	if transport == "http" {
		if fields, ok := input.(map[string]any); ok {
			delete(fields, "instance_id")
		}
	}
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if transport == "nats" {
		r, err := c.nats.RequestWithContext(ctx, "fixture.inbounds."+suffix, body)
		if err != nil {
			t.Fatal(err)
		}
		return r.Data
	}
	var reader io.Reader = bytes.NewReader(body)
	if method == "GET" {
		reader = nil
	}
	request, err := http.NewRequestWithContext(ctx, method, c.address+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	r, err := c.http.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("cacheable management response")
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func planInbound(t *testing.T, c clients, transport string, content []byte, disconnect bool) domain.ReplacementPlan {
	t.Helper()
	var data []byte
	if transport == "grpc" {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		p, err := v1.NewInboundsServiceClient(c.grpc).PlanInboundReplacement(ctx, &v1.PlanInboundReplacementRequest{InstanceId: "edge-a", Tag: "tunnel", Configuration: content, AllowInterruption: true, DisconnectExisting: disconnect})
		if err != nil {
			t.Fatal(err)
		}
		data, err = protojson.MarshalOptions{UseProtoNames: true}.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
	} else {
		data = jsonRequest(t, c, transport, "POST", "/v1/xray/proxies/instances/edge-a/inbounds/replacement-plans", "xray.proxies.inbounds.replacements.v1.plan", map[string]any{"instance_id": "edge-a", "tag": "tunnel", "configuration": content, "allow_interruption": true, "disconnect_existing": disconnect})
		// HTTP instance identity belongs only in the path.
	}
	var p jsonwire.ReplacementPlan
	if err := json.Unmarshal(data, &p); err != nil || p.Token == "" {
		t.Fatalf("plan %s %v", data, err)
	}
	return domain.ReplacementPlan{Token: p.Token, InstanceID: p.InstanceID, Generation: p.Generation, Tag: p.Tag, CurrentSHA256: p.CurrentSHA256, CandidateSHA256: p.CandidateSHA256, Mode: p.Mode, Reason: p.Reason, ExistingConnections: p.ExistingConnections, NewConnections: p.NewConnections, Fallback: p.Fallback, ExpiresAt: p.ExpiresAt}
}
func replaceInbound(t *testing.T, c clients, transport, token string) (*domain.ReplacementProgress, string, bool) {
	t.Helper()
	var data []byte
	if transport == "grpc" {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		result, err := v1.NewInboundsServiceClient(c.grpc).ReplaceInbound(ctx, &v1.ReplaceInboundRequest{InstanceId: "edge-a", Token: token})
		code := ""
		if err != nil {
			code = status.Convert(err).Message()
			for _, d := range status.Convert(err).Details() {
				if r, ok := d.(*v1.InboundOperation); ok {
					result = r
				}
			}
		}
		if result == nil {
			t.Fatal(err)
		}
		if result.Replacement == nil {
			return nil, code, result.RequiresInstanceRestart
		}
		p := result.Replacement
		return &domain.ReplacementProgress{Token: p.Token, Remove: domain.ReplacementStep{Attempted: p.Remove.Attempted, Effect: domain.Effect(p.Remove.Effect), Acknowledged: p.Remove.Acknowledged}, Add: domain.ReplacementStep{Attempted: p.Add.Attempted, Effect: domain.Effect(p.Add.Effect), Acknowledged: p.Add.Acknowledged}, NextAction: p.NextAction}, code, result.RequiresInstanceRestart
	}
	input := map[string]any{"token": token}
	if transport == "nats" {
		input["instance_id"] = "edge-a"
	}
	data = jsonRequest(t, c, transport, "POST", "/v1/xray/proxies/instances/edge-a/inbounds/replacements", "xray.proxies.inbounds.replacements.v1.apply", input)
	var result struct {
		Operation struct {
			Replacement *jsonwire.ReplacementProgress `json:"replacement"`
			Requires    bool                          `json:"requires_instance_restart"`
		} `json:"operation"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(string(data), err)
	}
	return progressDomain(result.Operation.Replacement), result.Error, result.Operation.Requires
}
func handlerBytes(t *testing.T, port uint32) []byte {
	t.Helper()
	config := &core.InboundHandlerConfig{Tag: "tunnel", ReceiverSettings: serial.ToTypedMessage(&proxyman.ReceiverConfig{Listen: xnet.NewIPOrDomain(xnet.LocalHostIP), PortList: &xnet.PortList{Range: []*xnet.PortRange{{From: port, To: port}}}}), ProxySettings: serial.ToTypedMessage(&xhttp.ServerConfig{})}
	data, err := proto.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func reserveTCP(t *testing.T) *net.TCPListener {
	t.Helper()
	l, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l
}
func openTunnel(t *testing.T, address string, target *net.TCPListener) (func(string), func()) {
	t.Helper()
	client, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	client.SetDeadline(time.Now().Add(8 * time.Second))
	target.SetDeadline(time.Now().Add(time.Second))
	if _, err := fmt.Fprintf(client, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target.Addr(), target.Addr()); err != nil {
		t.Fatal(err)
	}
	upstream, err := target.AcceptTCP()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { upstream.Close() })
	upstream.SetDeadline(time.Now().Add(8 * time.Second))
	response, err := http.ReadResponse(bufio.NewReader(client), &http.Request{Method: "CONNECT"})
	if err != nil || response.StatusCode != 200 {
		t.Fatal(response, err)
	}
	transfer := func(marker string) {
		t.Helper()
		client.SetDeadline(time.Now().Add(time.Second))
		upstream.SetDeadline(time.Now().Add(time.Second))
		if _, err := io.WriteString(client, marker); err != nil {
			t.Fatal(err)
		}
		buffer := make([]byte, len(marker))
		if _, err := io.ReadFull(upstream, buffer); err != nil || string(buffer) != marker {
			t.Fatal("established tunnel did not survive", string(buffer), err)
		}
	}
	return transfer, func() {
		t.Helper()
		upstream.SetDeadline(time.Now().Add(time.Second))
		var b [1]byte
		_, err := upstream.Read(b[:])
		if err == nil {
			t.Fatal("restart retained old tunnel")
		}
		if timed, ok := err.(net.Error); ok && timed.Timeout() {
			t.Fatal("restart did not close old tunnel", err)
		}
	}
}
func fallbackConfig(t *testing.T, c clients, transport string, sources []byte) {
	t.Helper()
	if transport == "grpc" {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		revision := uint64(0)
		result, err := configsv1.NewConfigurationsServiceClient(c.grpc).ReplaceConfiguration(ctx, &configsv1.ReplaceConfigurationRequest{InstanceId: "edge-a", ExpectedRevision: &revision, Sources: []*configsv1.ConfigurationSource{{Name: "config.json", Content: sources}}})
		if err != nil || !result.Persistence.Durable || !result.Persistence.Published || result.Execution == nil {
			t.Fatal(result, err)
		}
		return
	}
	input := map[string]any{"expected_revision": "0", "sources": []any{map[string]any{"name": "config.json", "content": sources}}}
	if transport == "nats" {
		input["instance_id"] = "edge-a"
	}
	data := jsonRequest(t, c, transport, "PUT", "/v1/xray/runtime/instances/edge-a/configuration", "xray.runtime.configs.v1.replace", input)
	var result map[string]json.RawMessage
	if err := json.Unmarshal(data, &result); err != nil || result["error"] != nil {
		t.Fatalf("fallback %s %v", data, err)
	}
	if result["persistence"] == nil || result["execution"] == nil {
		t.Fatal("missing config effects", string(data))
	}
}
func assertNoListener(t *testing.T, address string) {
	t.Helper()
	if c, err := net.DialTimeout("tcp", address, 100*time.Millisecond); err == nil {
		c.Close()
		t.Fatal("old listener still accepts", address)
	}
}

func applyFallbackConfig(t *testing.T, c clients, transport string) {
	t.Helper()
	if transport == "grpc" {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		revision := uint64(1)
		result, err := configsv1.NewConfigurationsServiceClient(c.grpc).ApplyConfiguration(ctx, &configsv1.ApplyConfigurationRequest{InstanceId: "edge-a", ExpectedRevision: &revision})
		if err != nil || result.Execution == nil {
			t.Fatal(result, err)
		}
		return
	}
	input := map[string]any{"expected_revision": "1"}
	if transport == "nats" {
		input["instance_id"] = "edge-a"
	}
	data := jsonRequest(t, c, transport, "POST", "/v1/xray/runtime/instances/edge-a/configuration/applications", "xray.runtime.configs.v1.apply", input)
	var result map[string]json.RawMessage
	if err := json.Unmarshal(data, &result); err != nil || result["error"] != nil || result["execution"] == nil {
		t.Fatal("apply fallback", string(data), err)
	}
}

func progressDomain(p *jsonwire.ReplacementProgress) *domain.ReplacementProgress {
	if p == nil {
		return nil
	}
	return &domain.ReplacementProgress{Token: p.Token, Remove: domain.ReplacementStep{Attempted: p.Remove.Attempted, Effect: p.Remove.Effect, Acknowledged: p.Remove.Acknowledged}, Add: domain.ReplacementStep{Attempted: p.Add.Attempted, Effect: p.Add.Effect, Acknowledged: p.Add.Acknowledged}, NextAction: p.NextAction}
}

func replacementStatus(t *testing.T, c clients, transport string) (string, bool, uint64) {
	t.Helper()
	var data []byte
	if transport == "grpc" {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		result, err := v1.NewInboundsServiceClient(c.grpc).GetInboundControlStatus(ctx, &v1.GetInboundControlStatusRequest{InstanceId: "edge-a"})
		if err != nil {
			t.Fatal(err)
		}
		data, err = protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
	} else {
		data = jsonRequest(t, c, transport, "GET", "/v1/xray/proxies/instances/edge-a/inbounds/control-status", "xray.proxies.inbounds.status.v1.get", map[string]any{"instance_id": "edge-a"})
	}
	var result struct {
		Requires   bool   `json:"requires_instance_restart"`
		Generation uint64 `json:"generation,string"`
		Last       struct {
			Effect      string                        `json:"effect"`
			Replacement *jsonwire.ReplacementProgress `json:"replacement"`
		} `json:"last_operation"`
	}
	if err := json.Unmarshal(data, &result); err != nil || result.Last.Replacement == nil {
		t.Fatal("missing retained replacement evidence", string(data), err)
	}
	return result.Last.Replacement.Token, result.Requires, result.Generation
}
