//go:build integration && linux

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	v1 "github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/grpcapi/proto/v1"
	configsv1 "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/testutil"
	"google.golang.org/grpc/status"
)

func TestOutboundSourcesAcrossIndependentTransports(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := brokerFixture(t, pki)
	for _, transport := range []string{"http", "grpc", "nats"} {
		t.Run(transport, func(t *testing.T) {
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "outbound-traffic") }))
			defer target.Close()
			f := newFixture(t, pki, broker, transport)
			f.config.Instances[0].Xray.WorkDirectory = t.TempDir()
			f.config.Communications.Requests.Timeout = 2 * time.Second
			if f.config.Communications.HTTP != nil {
				f.config.Communications.HTTP.WriteTimeout = 3 * time.Second
			}
			address := configureProbe(t, f, true)
			c := startNode(t, f)
			awaitNative(t, f.native)
			proxy, _ := url.Parse("http://" + address)
			tr := &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}
			defer tr.CloseIdleConnections()
			traffic := &http.Client{Transport: tr, Timeout: time.Second}
			for _, format := range []string{"json", "yaml", "toml"} {
				t.Run(format, func(t *testing.T) {
					var source string
					switch format {
					case "json":
						source = `{"outbounds":[{"tag":"managed","protocol":"freedom"},{"tag":"excluded","protocol":"blackhole"}]}`
					case "yaml":
						source = "outbounds:\n  - tag: managed\n    protocol: freedom\n"
					case "toml":
						source = "[[outbounds]]\ntag = 'managed'\nprotocol = 'freedom'\n"
					}
					result, code := sourceMutation(t, c, transport, "outbound."+format, "managed", []byte(source))
					if code != "" || result.Effect != "completed" || !result.Acknowledged {
						t.Fatal(result, code)
					}
					requireTraffic(t, traffic, target.URL, true)
					observation := readOutbounds(t, c, transport)
					for _, record := range observation.Outbounds {
						if record.Tag == "excluded" {
							t.Fatal("unselected outbound was applied")
						}
					}
					result, code = nativeMutation(t, c, transport, "remove", "managed", nil)
					if code != "" || !result.Acknowledged {
						t.Fatal(result, code)
					}
				})
			}
			for _, source := range []string{`{malformed`, `{"outbounds":[{"tag":"other","protocol":"freedom"}]}`} {
				result, code := sourceMutation(t, c, transport, "outbound.json", "managed", []byte(source))
				if code != "invalid_argument" || result.Effect != "not_applied" || result.Acknowledged {
					t.Fatal(result, code)
				}
			}
		})
	}
}
func sourceMutation(t *testing.T, c clients, transport, name, tag string, content []byte) (operation, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if transport == "grpc" {
		result, err := v1.NewOutboundsServiceClient(c.grpc).AddOutboundFromSources(ctx, &v1.AddOutboundFromSourcesRequest{InstanceId: "edge-a", Tag: tag, Sources: []*configsv1.ConfigurationSource{{Name: name, Content: content}}})
		if err != nil {
			for _, detail := range status.Convert(err).Details() {
				if result, ok := detail.(*v1.OutboundOperation); ok {
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
		request, _ := http.NewRequestWithContext(ctx, "POST", c.address+"/v1/xray/proxies/instances/edge-a/outbounds", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response, err := c.http.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		data, _ = io.ReadAll(response.Body)
		response.Body.Close()
	} else {
		response, err := c.nats.RequestWithContext(ctx, "fixture.outbounds.xray.proxies.outbounds.sources.v1.add", body)
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
