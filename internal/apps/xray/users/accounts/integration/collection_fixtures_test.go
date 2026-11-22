//go:build integration && linux

package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	v1 "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/grpcapi/proto/v1"
	handlers "github.com/xtls/xray-core/app/proxyman/command"
	stats "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// Real native calls are forwarded. Only mutation replies can be withheld, so
// compatibility reads succeed before exercising an actually applied lost ACK.
type nativeAccountRelay struct {
	handlers.UnimplementedHandlerServiceServer
	native                                   handlers.HandlerServiceClient
	drop                                     atomic.Bool
	lists, users, alters, bytes, nanoseconds atomic.Int64
}

func (r *nativeAccountRelay) ListInbounds(ctx context.Context, input *handlers.ListInboundsRequest) (*handlers.ListInboundsResponse, error) {
	r.lists.Add(1)
	return r.native.ListInbounds(ctx, input, grpc.MaxCallRecvMsgSize(32<<20))
}
func (r *nativeAccountRelay) GetInboundUsers(ctx context.Context, input *handlers.GetInboundUserRequest) (*handlers.GetInboundUserResponse, error) {
	started := time.Now()
	reply, err := r.native.GetInboundUsers(ctx, input, grpc.MaxCallRecvMsgSize(32<<20))
	r.users.Add(1)
	r.nanoseconds.Store(int64(time.Since(started)))
	if reply != nil {
		r.bytes.Store(int64(proto.Size(reply)))
	}
	return reply, err
}
func (r *nativeAccountRelay) GetInboundUsersCount(ctx context.Context, input *handlers.GetInboundUserRequest) (*handlers.GetInboundUsersCountResponse, error) {
	return r.native.GetInboundUsersCount(ctx, input)
}
func (r *nativeAccountRelay) AlterInbound(ctx context.Context, input *handlers.AlterInboundRequest) (*handlers.AlterInboundResponse, error) {
	r.alters.Add(1)
	reply, err := r.native.AlterInbound(ctx, input)
	if err == nil && r.drop.Load() {
		<-ctx.Done()
		return nil, status.FromContextError(ctx.Err()).Err()
	}
	return reply, err
}

type accountStatsRelay struct {
	stats.UnimplementedStatsServiceServer
	native stats.StatsServiceClient
}

func (r accountStatsRelay) GetSysStats(ctx context.Context, input *stats.SysStatsRequest) (*stats.SysStatsResponse, error) {
	return r.native.GetSysStats(ctx, input)
}
func accountRelay(t *testing.T, target string) (string, *nativeAccountRelay) {
	t.Helper()
	connection, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer(grpc.MaxSendMsgSize(32 << 20))
	relay := &nativeAccountRelay{native: handlers.NewHandlerServiceClient(connection)}
	handlers.RegisterHandlerServiceServer(server, relay)
	stats.RegisterStatsServiceServer(server, accountStatsRelay{native: stats.NewStatsServiceClient(connection)})
	done := make(chan struct{})
	go func() { defer close(done); server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); <-done; connection.Close() })
	return listener.Addr().String(), relay
}
func lostAccountReplies(t *testing.T, target string) (string, *atomic.Bool) {
	address, relay := accountRelay(t, target)
	relay.drop.Store(true)
	return address, &relay.drop
}

func collectionSource(t testing.TB, f fixture, count, emailBytes int) {
	t.Helper()
	source, err := os.ReadFile(f.config.Instances[0].Xray.Configuration)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(source, &config); err != nil {
		t.Fatal(err)
	}
	inbound := config["inbounds"].([]any)[0].(map[string]any)
	settings := inbound["settings"].(map[string]any)
	users := make([]any, count)
	for i := range users {
		users[i] = map[string]any{"email": fmt.Sprintf("%s-%05d@example.test", strings.Repeat("x", emailBytes), i), "id": fmt.Sprintf("00000000-0000-4000-8000-%012x", i+1), "level": i % 10}
	}
	settings["clients"] = users
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.config.Instances[0].Xray.Configuration, data, 0600); err != nil {
		t.Fatal(err)
	}
}

type accountViewPage struct {
	Observation struct {
		Generation uint64    `json:"generation,string"`
		Observed   time.Time `json:"observed_at"`
	} `json:"observation"`
	Users []struct {
		Email         string `json:"email"`
		Level         uint32 `json:"level"`
		AccountType   string `json:"account_type"`
		Configuration []byte `json:"configuration"`
	} `json:"users"`
	Next    string    `json:"next_cursor"`
	Expires time.Time `json:"expires_at"`
	Cost    struct {
		Bytes    uint64 `json:"native_payload_bytes,string"`
		Limit    uint64 `json:"native_limit_bytes,string"`
		Duration int64  `json:"native_duration_ns,string"`
	} `json:"read_cost"`
	Error string `json:"error"`
}

func accountViewData(t *testing.T, c clients, transport, view, tag, cursor string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if transport == "grpc" {
		client := v1.NewAccountsServiceClient(c.grpc)
		var message proto.Message
		var err error
		if view == "export" {
			message, err = client.ExportAccounts(ctx, &v1.ExportAccountsRequest{InstanceId: "edge-a", InboundTag: tag, Cursor: cursor})
		} else {
			message, err = client.ListAccountMetadata(ctx, &v1.ListAccountMetadataRequest{InstanceId: "edge-a", InboundTag: tag, Cursor: cursor})
		}
		if err != nil {
			data, _ := json.Marshal(map[string]string{"error": status.Convert(err).Message()})
			return data
		}
		data, err := protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	if transport == "http" {
		path := "/accounts"
		if view == "export" {
			path += "/exports"
		}
		path += "?inbound_tag=" + url.QueryEscape(tag)
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		response, err := c.http.Get(c.address + "/v1/xray/users/instances/edge-a" + path)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if response.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("cacheable account view")
		}
		return data
	}
	input := map[string]any{"instance_id": "edge-a", "inbound_tag": tag}
	if cursor != "" {
		input["cursor"] = cursor
	}
	body, _ := json.Marshal(input)
	suffix := "metadata.v1.list"
	if view == "export" {
		suffix = "exports.v1.list"
	}
	reply, err := c.nats.RequestWithContext(ctx, "fixture.accounts.xray.users.accounts."+suffix, body)
	if err != nil {
		t.Fatal(err)
	}
	return reply.Data
}
func accountView(t *testing.T, c clients, transport, view, tag, cursor string) accountViewPage {
	t.Helper()
	data := accountViewData(t, c, transport, view, tag, cursor)
	var p accountViewPage
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatal(string(data), err)
	}
	return p
}
