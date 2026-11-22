//go:build integration && linux

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	v1 "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/testutil"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func sourceAccount(t *testing.T, c clients, transport, name, email string, content []byte) (operation, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if transport == "grpc" {
		result, err := v1.NewAccountsServiceClient(c.grpc).AddAccountFromSources(ctx, &v1.AddAccountFromSourcesRequest{InstanceId: "edge-a", InboundTag: "members", Email: proto.String(email), Sources: []*v1.AccountSource{{Name: name, Content: content}}})
		if err != nil {
			for _, detail := range status.Convert(err).Details() {
				if result, ok := detail.(*v1.AccountOperation); ok {
					return operation{result.InboundTag, result.Email, result.Generation, result.Effect, result.Acknowledged, result.RequiresInstanceRestart, result.Kind}, status.Convert(err).Message()
				}
			}
			t.Fatal(err)
		}
		return operation{result.InboundTag, result.Email, result.Generation, result.Effect, result.Acknowledged, result.RequiresInstanceRestart, result.Kind}, ""
	}
	input := map[string]any{"email": email, "sources": []any{map[string]any{"name": name, "content": content}}}
	if transport == "nats" {
		input["instance_id"] = "edge-a"
		input["inbound_tag"] = "members"
	}
	body, _ := json.Marshal(input)
	var data []byte
	if transport == "http" {
		request, _ := http.NewRequestWithContext(ctx, "POST", c.address+"/v1/xray/users/instances/edge-a/accounts?inbound_tag=members", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response, err := c.http.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		data, err = io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if response.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("source mutation response is cacheable")
		}
	} else {
		response, err := c.nats.RequestWithContext(ctx, "fixture.accounts.xray.users.accounts.sources.v1.add", body)
		if err != nil {
			t.Fatal(err)
		}
		data = response.Data
	}
	var result struct {
		Operation operation
		Error     string
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(string(data), err)
	}
	return result.Operation, result.Error
}
func TestSourceAccountAddition(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := brokerFixture(t, pki)
	for _, transport := range []string{"http", "grpc", "nats"} {
		t.Run(transport, func(t *testing.T) {
			target := accountTarget(t)
			f := newFixture(t, pki, broker, transport)
			address := allowAccountTraffic(t, f, target.Listener.Addr().String())
			f.config.Instances[0].Xray.WorkDirectory = t.TempDir()
			f.config.Communications.Requests.Timeout = 2 * time.Second
			if f.config.Communications.HTTP != nil {
				f.config.Communications.HTTP.WriteTimeout = 3 * time.Second
			}
			c := startNode(t, f)
			awaitNative(t, f.native)
			for _, format := range []string{"json", "yaml", "toml"} {
				t.Run(format, func(t *testing.T) {
					exerciseSourceAccount(t, c, transport, format, address, target.Listener.Addr().String())
				})
			}
			for _, source := range []string{
				`{malformed`,
				`{"inbounds":[{"tag":"other","port":1,"protocol":"vless","settings":{"decryption":"none","clients":[{"email":"managed@example.test","id":"13dd1692-3aa7-4a42-855a-6b203286c515"}]}}]}`,
				`{"inbounds":[{"tag":"members","port":1,"protocol":"vless","settings":{"decryption":"none","clients":[{"email":"other@example.test","id":"13dd1692-3aa7-4a42-855a-6b203286c515"}]}}]}`,
			} {
				result, code := sourceAccount(t, c, transport, "account.json", "managed@example.test", []byte(source))
				if code != "invalid_argument" || result.Effect != "not_applied" || result.Acknowledged {
					t.Fatal(result, code)
				}
			}
			var count countResponse
			if err := json.Unmarshal(countData(t, c, transport), &count); err != nil || count.Count != 2 {
				t.Fatal(count, err)
			}
		})
	}
}
func exerciseSourceAccount(t *testing.T, c clients, transport, format, address, target string) {
	t.Helper()
	var source string
	switch format {
	case "json":
		source = `{"inbounds":[{"tag":"members","port":1,"protocol":"vless","settings":{"decryption":"none","clients":[{"email":"managed@example.test","id":"13dd1692-3aa7-4a42-855a-6b203286c515"},{"email":"excluded@example.test","id":"e1b17cda-6358-4bb5-8f8e-e987df72d769"}]}},{"tag":"excluded","port":1,"protocol":"http"}]}`
	case "yaml":
		source = "inbounds:\n  - tag: members\n    port: 1\n    protocol: vless\n    settings:\n      decryption: none\n      clients:\n        - email: managed@example.test\n          id: 13dd1692-3aa7-4a42-855a-6b203286c515\n"
	case "toml":
		source = "[[inbounds]]\ntag = 'members'\nport = 1\nprotocol = 'vless'\n[inbounds.settings]\ndecryption = 'none'\n[[inbounds.settings.clients]]\nemail = 'managed@example.test'\nid = '13dd1692-3aa7-4a42-855a-6b203286c515'\n"
	}
	result, code := sourceAccount(t, c, transport, "account."+format, "managed@example.test", []byte(source))
	if code != "" || !result.Acknowledged || result.Effect != "completed" || result.Email != "managed@example.test" {
		t.Fatal(result, code)
	}
	var count countResponse
	if err := json.Unmarshal(countData(t, c, transport), &count); err != nil || count.Count != 3 {
		t.Fatal("unselected accounts were applied", count, err)
	}
	tunnel, _, err := accountTunnel(address, managedID, target)
	if err != nil {
		t.Fatal("source-selected account did not authenticate", err)
	}
	tunnel.Close()
	result, code = accountMutation(t, c, transport, "remove", "managed@example.test", nil)
	if code != "" || !result.Acknowledged {
		t.Fatal(result, code)
	}
	if tunnel, _, err := accountTunnel(address, managedID, target); err == nil {
		tunnel.Close()
		t.Fatal("removed source account still authenticates")
	}
}
