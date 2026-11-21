//go:build integration && linux

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"
	"time"

	v1 "github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/grpcapi/proto/v1"
	configsv1 "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/testutil"
	"google.golang.org/grpc/status"
)

func TestNativeSourceAddition(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := brokerFixture(t, pki)
	for _, transport := range []string{"http", "grpc", "nats"} {
		t.Run(transport, func(t *testing.T) {
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "source-inbound") }))
			defer target.Close()
			f := newFixture(t, pki, broker, transport)
			f.config.Instances[0].Xray.WorkDirectory = t.TempDir()
			f.config.Communications.Requests.Timeout = 2 * time.Second
			if f.config.Communications.HTTP != nil {
				f.config.Communications.HTTP.WriteTimeout = 3 * time.Second
			}
			c := startNode(t, f)
			awaitNative(t, f.native)
			for _, format := range []string{"json", "yaml", "toml"} {
				t.Run(format, func(t *testing.T) { exerciseSourceAddition(t, c, transport, format, target.URL) })
			}
			for _, source := range []string{`{malformed`, `{"inbounds":[{"tag":"another","protocol":"http"}]}`} {
				result, code := sourceMutation(t, c, transport, "inbound.json", "source-http", []byte(source))
				if code != "invalid_argument" || result.Effect != "not_applied" || result.Acknowledged || result.RequiresInstanceRestart {
					t.Fatal(result, code)
				}
			}
		})
	}
}
func exerciseSourceAddition(t *testing.T, c clients, transport, format, target string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	var source string
	switch format {
	case "json":
		source = fmt.Sprintf(`{"outbounds":[{"tag":"excluded-outbound","protocol":"blackhole"}],"inbounds":[{"tag":"source-http","listen":"127.0.0.1","port":%d,"protocol":"http","settings":{}},{"tag":"excluded-inbound","listen":"127.0.0.1","port":1,"protocol":"http"}]}`, port)
	case "yaml":
		source = fmt.Sprintf("inbounds:\n  - tag: source-http\n    listen: 127.0.0.1\n    port: %d\n    protocol: http\n    settings: {}\n", port)
	case "toml":
		source = fmt.Sprintf("[[inbounds]]\ntag = 'source-http'\nlisten = '127.0.0.1'\nport = %d\nprotocol = 'http'\n[inbounds.settings]\n", port)
	}
	result, code := sourceMutation(t, c, transport, "inbound."+format, "source-http", []byte(source))
	if code != "" || result.Effect != "completed" || !result.Acknowledged || result.Tag != "source-http" {
		t.Fatal(result, code)
	}
	proxy, _ := url.Parse("http://" + address)
	tr := &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}
	defer tr.CloseIdleConnections()
	traffic := &http.Client{Transport: tr, Timeout: time.Second}
	response, err := traffic.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if string(data) != "source-inbound" {
		t.Fatalf("traffic %q", data)
	}
	_, tags := nativeRecords(t, c, transport, true)
	if !slices.Contains(tags, "source-http") || slices.Contains(tags, "excluded-inbound") {
		t.Fatal("selection escaped", tags)
	}
	result, code = nativeMutation(t, c, transport, "remove", "source-http", nil)
	if code != "" || result.Effect != "completed" {
		t.Fatal(result, code)
	}
}
func sourceMutation(t *testing.T, c clients, transport, name, tag string, content []byte) (operation, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if transport == "grpc" {
		result, err := v1.NewInboundsServiceClient(c.grpc).AddInboundFromSources(ctx, &v1.AddInboundFromSourcesRequest{InstanceId: "edge-a", Tag: tag, Sources: []*configsv1.ConfigurationSource{{Name: name, Content: content}}})
		if err != nil {
			for _, detail := range status.Convert(err).Details() {
				if result, ok := detail.(*v1.InboundOperation); ok {
					return operation{result.Tag, result.Generation, result.Effect, result.Acknowledged, result.RequiresInstanceRestart}, status.Convert(err).Message()
				}
			}
			t.Fatal(err)
		}
		return operation{result.Tag, result.Generation, result.Effect, result.Acknowledged, result.RequiresInstanceRestart}, ""
	}
	input := map[string]any{"tag": tag, "sources": []any{map[string]any{"name": name, "content": content}}}
	if transport == "nats" {
		input["instance_id"] = "edge-a"
	}
	body, _ := json.Marshal(input)
	var data []byte
	if transport == "http" {
		request, _ := http.NewRequestWithContext(ctx, "POST", c.address+"/v1/xray/proxies/instances/edge-a/inbounds", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response, err := c.http.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		data, _ = io.ReadAll(response.Body)
		response.Body.Close()
	} else {
		response, err := c.nats.RequestWithContext(ctx, "fixture.inbounds.xray.proxies.inbounds.sources.v1.add", body)
		if err != nil {
			t.Fatal(err)
		}
		data = response.Data
	}
	var result struct {
		Operation operation `json:"operation"`
		Error     string    `json:"error"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(string(data), err)
	}
	return result.Operation, result.Error
}

func TestLiveSourceAdditionDoesNotBecomeDesiredState(t *testing.T) {
	f := newFixture(t, testutil.NewPKI(t), "", "http")
	f.config.Instances[0].Xray.WorkDirectory = t.TempDir()
	f.config.Communications.Requests.Timeout = 2 * time.Second
	f.config.Communications.HTTP.WriteTimeout = 3 * time.Second
	c := startNode(t, f)
	awaitNative(t, f.native)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	source := fmt.Sprintf(`{"inbounds":[{"tag":"temporary","listen":"127.0.0.1","port":%d,"protocol":"http"}]}`, port)
	result, code := sourceMutation(t, c, "http", "inbound.json", "temporary", []byte(source))
	if code != "" || !result.Acknowledged {
		t.Fatal(result, code)
	}
	_, tags := nativeRecords(t, c, "http", true)
	if !slices.Contains(tags, "temporary") {
		t.Fatal(tags)
	}
	request, _ := http.NewRequest("POST", c.address+"/v1/core/engines/instances/edge-a/executions", nil)
	response, err := c.http.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("restart %d %s", response.StatusCode, data)
	}
	awaitNative(t, f.native)
	_, tags = nativeRecords(t, c, "http", true)
	if slices.Contains(tags, "temporary") {
		t.Fatal("live mutation was replayed as desired configuration", tags)
	}
}
