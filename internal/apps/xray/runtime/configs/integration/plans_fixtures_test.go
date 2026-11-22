//go:build integration && linux

package integration_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	v1 "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/grpcapi/proto/v1"
	instancesv1 "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/testutil"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type planResponse struct {
	ID          string `json:"plan_id"`
	Mode        string `json:"mode"`
	Reason      string `json:"reason"`
	Error       string `json:"error"`
	Retained    bool   `json:"connections_retained"`
	Revision    uint64 `json:"expected_revision,string"`
	ExecutionID string `json:"execution_id"`
	Generation  uint64 `json:"generation,string"`
}
type applicationResponse struct {
	ID                string `json:"application_id"`
	Outcome           string `json:"outcome"`
	Error             string `json:"error"`
	Effect            string `json:"engine_effect"`
	Readiness         string `json:"readiness"`
	Revision          uint64 `json:"revision,string"`
	Generation        uint64 `json:"generation,string"`
	RequiresRecovery  bool   `json:"requires_recovery"`
	ReceiptDurable    bool   `json:"receipt_durable"`
	RecoveryAvailable bool   `json:"recovery_available"`
	Persistence       struct {
		Published bool `json:"published"`
		Durable   bool `json:"durable"`
	} `json:"persistence"`
	Recovery *struct {
		ID        string `json:"recovery_id"`
		Revision  uint64 `json:"revision,string"`
		Outcome   string `json:"outcome"`
		Readiness string `json:"readiness"`
		Error     string `json:"error"`
	} `json:"recovery"`
}

func plannedFixture(t *testing.T, pki testutil.PKI, broker, transport string) (fixture, string) {
	t.Helper()
	f := nodeFixture(t, pki, broker, transport)
	native, err := net.Listen("tcp", "127.0.0.2:0")
	if err != nil {
		t.Fatal(err)
	}
	f.resources.listeners = append(f.resources.listeners, native)
	address := native.Addr().String()
	var root map[string]any
	if err := json.Unmarshal(f.original, &root); err != nil {
		t.Fatal(err)
	}
	root["api"] = map[string]any{"tag": "api", "listen": address, "services": []string{"StatsService", "HandlerService", "RoutingService"}}
	root["stats"] = map[string]any{}
	root["inbounds"].([]any)[0].(map[string]any)["tag"] = "probe"
	root["outbounds"] = []any{map[string]any{"tag": "allow", "protocol": "freedom"}, map[string]any{"tag": "deny", "protocol": "blackhole"}}
	root["routing"] = map[string]any{"rules": []any{map[string]any{"type": "field", "ruleTag": "initial", "inboundTag": []string{"probe"}, "outboundTag": "allow"}}}
	f.original, err = json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.directory, "active.json"), f.original, 0600); err != nil {
		t.Fatal(err)
	}
	f.config.Instances[0].Xray.API = &settings.XrayAPI{Network: "tcp", Address: address}
	f.config.Instances[0].Xray.StateDirectory = testutil.NewPrivateDirectory(t)
	f.config.Communications.Requests.Timeout = 2 * time.Second
	if f.config.Communications.HTTP != nil {
		f.config.Communications.HTTP.WriteTimeout = 4 * time.Second
	}
	return f, address
}
func routeSource(t *testing.T, f fixture, target string) []domain.Source {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal(f.original, &root); err != nil {
		t.Fatal(err)
	}
	root["routing"].(map[string]any)["rules"].([]any)[0].(map[string]any)["outboundTag"] = target
	content, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	return []domain.Source{{Name: "base.json", Content: content}}
}
func planCall(t *testing.T, c clients, transport, operation, id string, expected uint64, sources []domain.Source, recoveryID string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if transport == "grpc" {
		client := v1.NewConfigurationsServiceClient(c.grpc)
		var message proto.Message
		var err error
		switch operation {
		case "plan":
			r := &v1.PlanConfigurationRequest{InstanceId: "edge-a", ExpectedRevision: &expected}
			for _, source := range sources {
				r.Sources = append(r.Sources, &v1.ConfigurationSource{Name: source.Name, Content: source.Content})
			}
			message, err = client.PlanConfiguration(ctx, r)
		case "apply":
			message, err = client.ApplyConfigurationPlan(ctx, &v1.ApplyConfigurationPlanRequest{InstanceId: "edge-a", PlanId: id})
		case "get":
			message, err = client.GetConfigurationApplication(ctx, &v1.GetConfigurationApplicationRequest{InstanceId: "edge-a", ApplicationId: id})
		case "recover":
			message, err = client.RecoverConfiguration(ctx, &v1.RecoverConfigurationRequest{InstanceId: "edge-a", ApplicationId: id, ExpectedRevision: &expected, RecoveryId: recoveryID})
		}
		if err != nil {
			data, _ := json.Marshal(map[string]string{"error": status.Convert(err).Message()})
			return data
		}
		data, err := (protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}).Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	body := map[string]any{}
	if transport == "nats" {
		body["instance_id"] = "edge-a"
	}
	switch operation {
	case "plan":
		body["expected_revision"] = strconv.FormatUint(expected, 10)
		wire := []map[string]any{}
		for _, source := range sources {
			wire = append(wire, map[string]any{"name": source.Name, "content": source.Content})
		}
		body["sources"] = wire
	case "apply":
		if transport == "nats" {
			body["plan_id"] = id
		}
	case "get":
		if transport == "nats" {
			body["application_id"] = id
		}
	case "recover":
		if transport == "nats" {
			body["application_id"] = id
		}
		body["expected_revision"] = strconv.FormatUint(expected, 10)
		body["recovery_id"] = recoveryID
	}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if transport == "nats" {
		suffix := map[string]string{"plan": "plans.v1.create", "apply": "plans.v1.apply", "get": "applications.v1.get", "recover": "applications.v1.recover"}[operation]
		response, err := c.nats.RequestWithContext(ctx, "fixture.configs.xray.runtime.configs."+suffix, data)
		if err != nil {
			t.Fatal(err)
		}
		return response.Data
	}
	path, method := "/v1/xray/runtime/instances/edge-a/configuration/plans", "POST"
	switch operation {
	case "apply":
		path += "/" + id + "/applications"
	case "get":
		path = "/v1/xray/runtime/instances/edge-a/configuration/applications/" + id
		method = "GET"
	case "recover":
		path = "/v1/xray/runtime/instances/edge-a/configuration/applications/" + id + "/recoveries"
	}
	var payload io.Reader = bytes.NewReader(data)
	if method == "GET" {
		payload = nil
	}
	request, err := http.NewRequestWithContext(ctx, method, c.address+path, payload)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("cacheable config receipt")
	}
	data, err = io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func review(t *testing.T, c clients, transport string, expected uint64, sources []domain.Source) planResponse {
	t.Helper()
	data := planCall(t, c, transport, "plan", "", expected, sources, "")
	var result planResponse
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if result.Error != "" {
		t.Fatal(string(data))
	}
	return result
}
func application(t *testing.T, c clients, transport, operation, id string, expected uint64, recovery string) applicationResponse {
	t.Helper()
	data := planCall(t, c, transport, operation, id, expected, nil, recovery)
	var result applicationResponse
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	return result
}
func execution(t *testing.T, c clients) (string, uint64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if c.grpc != nil {
		r, err := instancesv1.NewInstancesServiceClient(c.grpc).GetInstanceStatus(ctx, &instancesv1.GetInstanceStatusRequest{InstanceId: "edge-a"})
		if err != nil {
			t.Fatal(err)
		}
		return r.ExecutionId, r.Generation
	}
	var data []byte
	if c.http != nil {
		response, err := c.http.Get(c.address + "/v1/core/engines/instances/edge-a")
		if err != nil {
			t.Fatal(err)
		}
		data, err = io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
	} else {
		response, err := c.nats.RequestWithContext(ctx, "fixture.configs.core.engines.instances.status.v1.get", []byte(`{"instance_id":"edge-a"}`))
		if err != nil {
			t.Fatal(err)
		}
		data = response.Data
	}
	var r struct {
		ID         string `json:"execution_id"`
		Generation uint64 `json:"generation,string"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	return r.ID, r.Generation
}
func connectedTraffic(t *testing.T, proxy string) (net.Conn, net.Conn) {
	t.Helper()
	target, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { target.Close() })
	client, err := net.DialTimeout("tcp", proxy, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	client.SetDeadline(time.Now().Add(time.Second))
	if _, err := io.WriteString(client, "CONNECT "+target.Addr().String()+" HTTP/1.1\r\nHost: "+target.Addr().String()+"\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	target.(*net.TCPListener).SetDeadline(time.Now().Add(time.Second))
	server, err := target.Accept()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	response, err := http.ReadResponse(bufio.NewReader(client), &http.Request{Method: "CONNECT"})
	if err != nil || response.StatusCode != 200 {
		t.Fatal(response, err)
	}
	return client, server
}
func payload(t *testing.T, client, server net.Conn, text string) {
	t.Helper()
	client.SetDeadline(time.Now().Add(time.Second))
	server.SetDeadline(time.Now().Add(time.Second))
	if _, err := io.WriteString(client, text); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(text))
	if _, err := io.ReadFull(server, got); err != nil || string(got) != text {
		t.Fatal(string(got), err)
	}
	if _, err := io.WriteString(server, text); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(client, got); err != nil || string(got) != text {
		t.Fatal(string(got), err)
	}
}
