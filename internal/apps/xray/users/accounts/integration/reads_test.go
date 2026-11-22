//go:build integration && linux

package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"testing"
	"time"

	v1 "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/testutil"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/proxy/vless"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type countResponse struct {
	Observation struct {
		InstanceID string    `json:"instance_id"`
		InboundTag string    `json:"inbound_tag"`
		Generation uint64    `json:"generation,string"`
		ObservedAt time.Time `json:"observed_at"`
	}
	Count int64 `json:"count,string"`
}
type accountResponse struct {
	Observation struct {
		InstanceID string `json:"instance_id"`
		InboundTag string `json:"inbound_tag"`
		Generation uint64 `json:"generation,string"`
	}
	RequestedEmail string `json:"requested_email"`
	User           *struct {
		Email         string
		Level         uint32
		Configuration []byte
	}
}

func TestNativeAccountReadsAcrossTransports(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := brokerFixture(t, pki)
	for _, transport := range []string{"http", "grpc", "nats"} {
		t.Run(transport, func(t *testing.T) {
			f := newFixture(t, pki, broker, transport)
			c := startNode(t, f)
			awaitNative(t, f.native)
			assertNativeAccounts(t, c, transport)
		})
	}
}
func countData(t *testing.T, c clients, transport string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	switch transport {
	case "http":
		return readHTTP(t, c, "/accounts/count?inbound_tag=members")
	case "grpc":
		result, err := v1.NewAccountsServiceClient(c.grpc).GetAccountCount(ctx, &v1.GetAccountCountRequest{InstanceId: "edge-a", InboundTag: "members"})
		if err != nil {
			t.Fatal(err)
		}
		data, err := protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		return data
	case "nats":
		response, err := c.nats.RequestWithContext(ctx, "fixture.accounts.xray.users.accounts.count.v1.get", []byte(`{"instance_id":"edge-a","inbound_tag":"members"}`))
		if err != nil {
			t.Fatal(err)
		}
		return response.Data
	default:
		t.Fatal("unknown test transport")
		return nil
	}
}
func accountData(t *testing.T, c clients, transport, email string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	switch transport {
	case "http":
		return readHTTP(t, c, "/account?inbound_tag=members&email="+url.QueryEscape(email))
	case "grpc":
		result, err := v1.NewAccountsServiceClient(c.grpc).GetAccount(ctx, &v1.GetAccountRequest{InstanceId: "edge-a", InboundTag: "members", Email: email})
		if err != nil {
			t.Fatal(err)
		}
		data, err := protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		return data
	case "nats":
		body, _ := json.Marshal(map[string]string{"instance_id": "edge-a", "inbound_tag": "members", "email": email})
		response, err := c.nats.RequestWithContext(ctx, "fixture.accounts.xray.users.accounts.v1.get", body)
		if err != nil {
			t.Fatal(err)
		}
		return response.Data
	default:
		t.Fatal("unknown test transport")
		return nil
	}
}
func readHTTP(t *testing.T, c clients, suffix string) []byte {
	t.Helper()
	response, err := c.http.Get(c.address + "/v1/xray/users/instances/edge-a" + suffix)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("account read status=%d %s", response.StatusCode, data)
	}
	return data
}

func assertNativeAccounts(t *testing.T, c clients, transport string) {
	t.Helper()
	var count countResponse
	if err := json.Unmarshal(countData(t, c, transport), &count); err != nil {
		t.Fatal(err)
	}
	if count.Count != 2 || count.Observation.InstanceID != "edge-a" || count.Observation.InboundTag != "members" || count.Observation.Generation != 1 || count.Observation.ObservedAt.IsZero() {
		t.Fatal(count)
	}
	for _, email := range []string{"first@example.test", "missing@example.test", "FIRST@example.test"} {
		var result accountResponse
		if err := json.Unmarshal(accountData(t, c, transport, email), &result); err != nil {
			t.Fatal(err)
		}
		if result.RequestedEmail != email || result.Observation.Generation != 1 || result.Observation.InboundTag != "members" {
			t.Fatal(result)
		}
		if email == "missing@example.test" {
			if result.User != nil {
				t.Fatal("missing native account became a zero user", result)
			}
			continue
		}
		if result.User == nil || result.User.Email != "first@example.test" {
			t.Fatal(result)
		}
		var user protocol.User
		if err := proto.Unmarshal(result.User.Configuration, &user); err != nil || user.Account == nil || user.Email != result.User.Email {
			t.Fatal(&user, err)
		}
		var account vless.Account
		if err := proto.Unmarshal(user.Account.Value, &account); err != nil || account.Id != "a122228b-546c-4887-9638-08a744fb9074" {
			t.Fatal("native account lost", &account, err)
		}
	}
}

func TestLargeMembershipUsesFocusedReads(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := brokerFixture(t, pki)
	for _, size := range []int{0, 50, 51, 1000} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			f := newFixture(t, pki, broker, "all")
			content, err := os.ReadFile(f.config.Instances[0].Xray.Configuration)
			if err != nil {
				t.Fatal(err)
			}
			var configuration map[string]any
			if err := json.Unmarshal(content, &configuration); err != nil {
				t.Fatal(err)
			}
			settings := configuration["inbounds"].([]any)[0].(map[string]any)["settings"].(map[string]any)
			users := settings["clients"].([]any)
			if size < len(users) {
				users = users[:size]
			}
			for i := 2; i < size; i++ {
				users = append(users, map[string]any{"email": fmt.Sprintf("member-%d@example.test", i), "id": fmt.Sprintf("00000000-0000-4000-8000-%012x", i)})
			}
			settings["clients"] = users
			content, err = json.Marshal(configuration)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(f.config.Instances[0].Xray.Configuration, content, 0600); err != nil {
				t.Fatal(err)
			}
			c := startNode(t, f)
			awaitNative(t, f.native)
			for _, transport := range []string{"http", "grpc", "nats"} {
				var count countResponse
				if err := json.Unmarshal(countData(t, c, transport), &count); err != nil {
					t.Fatal(err)
				}
				if count.Count != int64(size) {
					t.Fatal(transport, count)
				}
				if size == 0 {
					continue
				}
				email := fmt.Sprintf("member-%d@example.test", size-1)
				var account accountResponse
				if err := json.Unmarshal(accountData(t, c, transport, email), &account); err != nil {
					t.Fatal(err)
				}
				if account.User == nil || account.User.Email != email {
					t.Fatal(transport, account)
				}
			}
		})
	}
}
func TestUnconfiguredAccountAPIIsExplicit(t *testing.T) {
	f := newFixture(t, testutil.NewPKI(t), "", "http")
	f.config.Instances[0].Xray.API = nil
	c := startNode(t, f)
	awaitNative(t, f.native)
	response, err := c.http.Get(c.address + "/v1/xray/users/instances/edge-a/accounts/count?inbound_tag=members")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	if response.StatusCode != 409 || string(data) != `{"error":"failed_precondition"}` {
		t.Fatal(response.StatusCode, string(data))
	}
}
