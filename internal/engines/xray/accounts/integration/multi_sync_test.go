package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"

	accountsv1 "github.com/PapilioVPN/node/internal/engines/xray/accounts/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/xray/accounts/membership"
	"github.com/PapilioVPN/node/internal/engines/xray/accounts/native"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
	"testing"
)

const instanceAccountsPath = "/v1/xray/instances/primary/accounts"

func TestSyncUserMovesAcrossSelectedInbounds(t *testing.T) {
	f := startFixture(t, configuredAccount{Email: "move", ID: credential}, configuredAccount{Email: "leave", ID: rotatedCredential})
	f.request(t, http.MethodPut, instanceAccountsPath+"/membership", map[string]any{
		"inbound_tags": []string{"vless", "trojan"},
		"email":        "move",
		"assignments":  []map[string]any{{"inbound_tag": "trojan", "credential": map[string]string{"protocol": "trojan", "password": "new"}}},
	}, http.StatusOK)
	users := f.nativeUsers(t, "vless")
	if len(users) != 1 || users[0].Email != "leave" {
		t.Fatalf("VLESS after move = %d users", len(users))
	}
	users = f.nativeUsers(t, "trojan")
	if len(users) != 1 || users[0].Email != "move" {
		t.Fatalf("Trojan after move = %d users", len(users))
	}
	f.request(t, http.MethodPut, instanceAccountsPath+"/membership", map[string]any{
		"inbound_tags": []string{"vless"},
		"email":        "move",
	}, http.StatusOK)
	if len(f.nativeUsers(t, "trojan")) != 1 {
		t.Fatal("unselected inbound lost its account")
	}
}

func TestSyncSelectedFullAndChunkedMembership(t *testing.T) {
	f := startFixture(t, configuredAccount{Email: "legacy", ID: credential})
	tags := []string{"vless", "trojan"}
	assignments := []*accountsv1.Assignment{
		{InboundTag: "vless", Credential: &accountsv1.Credential{Protocol: "vless", Uuid: credential}},
		{InboundTag: "trojan", Credential: &accountsv1.Credential{Protocol: "trojan", Password: "assigned"}},
	}
	snapshot := &accountsv1.SyncUsersRequest{Id: instanceID, InboundTags: tags,
		Snapshot: &accountsv1.VLESSAccounts{Accounts: []*accountsv1.VLESSAccount{{Email: "both", Assignments: assignments}}}}
	result, err := f.grpc.SyncUsers(t.Context(), snapshot)
	require(t, err)
	expectSync(t, result, 2, 1)
	assertNamed(t, f, "vless", "both")
	assertNamed(t, f, "trojan", "both")

	// An incompatible assignment to a later tag cannot remove from the first.
	snapshot.Snapshot.Accounts[0].Assignments[1].Credential = assignments[0].Credential
	result, err = f.grpc.SyncUsers(t.Context(), snapshot)
	require(t, err)
	if result.GetOutcome() != "not_applied" || result.GetFailure() != "wrong_protocol" {
		t.Fatalf("mismatched protocol: %v", result)
	}
	assertNamed(t, f, "vless", "both")
	assertNamed(t, f, "trojan", "both")

	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	stream, err := f.grpc.SyncUsersChunked(ctx)
	require(t, err)
	require(t, stream.Send(&accountsv1.UsersChunk{Id: instanceID, InboundTags: tags, Last: true,
		Accounts: []*accountsv1.VLESSAccount{{Email: "both", Assignments: []*accountsv1.Assignment{assignments[0]}}}}))
	result, err = stream.CloseAndRecv()
	require(t, err)
	expectSync(t, result, 0, 1)
	assertNamed(t, f, "vless", "both")
	assertNamed(t, f, "trojan")

	frame := `{"index":0,"last":true,"inbound_tags":["vless","trojan"],"accounts":[{"email":"both","assignments":[{"inbound_tag":"trojan","credential":{"protocol":"trojan","password":"moved"}}]}]}`
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPut, f.http.URL+instanceAccountsPath+"/membership/stream", bytes.NewBufferString(frame))
	require(t, err)
	reply, err := f.http.Client().Do(request)
	require(t, err)
	var sync membership.SyncResult
	require(t, json.NewDecoder(reply.Body).Decode(&sync))
	reply.Body.Close()
	if reply.StatusCode != http.StatusOK || sync.Outcome != membership.Confirmed || sync.Added != 1 || sync.Removed != 1 {
		t.Fatalf("HTTP delta = %d %+v", reply.StatusCode, sync)
	}
	assertNamed(t, f, "vless")
	assertNamed(t, f, "trojan", "both")

	f.request(t, http.MethodPut, instanceAccountsPath, map[string]any{"inbound_tags": []string{"vless"}, "accounts": []any{}}, http.StatusOK)
	assertNamed(t, f, "trojan", "both")

	incomplete, err := f.grpc.SyncUsersChunked(ctx)
	require(t, err)
	require(t, incomplete.Send(&accountsv1.UsersChunk{Id: instanceID, InboundTags: tags, Accounts: []*accountsv1.VLESSAccount{{Email: "both"}}}))
	if _, err := incomplete.CloseAndRecv(); status.Code(err) != codes.InvalidArgument {
		t.Fatal("incomplete gRPC stream did not fail")
	}
	assertNamed(t, f, "trojan", "both")
}

func TestMultiInboundSyncReportsUnknownWithEarlierAcknowledgement(t *testing.T) {
	f := startFixture(t, configuredAccount{Email: "move", ID: credential})
	service := f.interceptEdits(t, func(count int32) error {
		if count == 2 {
			return status.Error(codes.Unavailable, "intercepted native acknowledgement")
		}
		return nil
	})
	req := membership.SyncUserRequest{Scope: membership.Scope{InstanceID: instanceID}, InboundTags: []string{"vless", "trojan"},
		Email: "move", Assignments: []membership.Assignment{{InboundTag: "trojan", Credential: membership.Credential{Protocol: membership.Trojan, Password: "new"}}}}
	result, err := service.SyncUser(t.Context(), req)
	require(t, err)
	if result.Outcome != membership.Unknown || result.Removed != 1 || result.Added != 0 || result.Failure != "native_acknowledgement_unknown" {
		t.Fatalf("interrupted move = %+v", result)
	}
	assertNamed(t, f, "vless")
	assertNamed(t, f, "trojan", "move")
	result, err = service.SyncUser(t.Context(), req)
	require(t, err)
	if result.Outcome != membership.NotApplied || result.Failure != "fenced" {
		t.Fatalf("retry after unacknowledged effect = %+v", result)
	}
}

func assertNamed(t *testing.T, f fixture, tag string, names ...string) {
	t.Helper()
	remaining := make(map[string]bool, len(names))
	for _, name := range names {
		remaining[name] = true
	}
	for _, user := range f.nativeUsers(t, tag) {
		if !remaining[user.Email] {
			t.Fatalf("unexpected member %q in %s", user.Email, tag)
		}
		delete(remaining, user.Email)
	}
	if len(remaining) != 0 {
		t.Fatalf("missing members in %s: %v", tag, remaining)
	}
}

func TestMultiInboundSyncStopsAfterNativeRejection(t *testing.T) {
	f := startFixture(t, configuredAccount{Email: "move", ID: credential})
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	_, err := f.grpc.CountVLESSAccounts(ctx, &accountsv1.CountVLESSAccountsRequest{Id: instanceID, InboundTag: "vless"})
	require(t, err)

	// Change the engine through its private API after Node has inventoried it.
	// The duplicate email forces an actual Xray rejection on the second effect.
	wire, err := proto.Marshal(&native.PasswordAccount{Password: "private account"})
	require(t, err)
	body, err := proto.Marshal(&native.AddUserOperation{User: &native.User{Email: "move",
		Account: &native.TypedMessage{Type: "xray.proxy.trojan.Account", Value: wire}}})
	require(t, err)
	conn, err := f.instance.NativeConnection(instanceID)
	require(t, err)
	require(t, conn.Invoke(ctx, native.AlterInboundMethod, &native.AlterInboundRequest{Tag: "trojan",
		Operation: &native.TypedMessage{Type: native.AddUserOperationType, Value: body}}, &emptypb.Empty{}))

	stream, err := f.grpc.SyncUsersChunked(ctx)
	require(t, err)
	require(t, stream.Send(&accountsv1.UsersChunk{Id: instanceID, InboundTags: []string{"vless", "trojan"}, Last: true,
		Accounts: []*accountsv1.VLESSAccount{
			{Email: "move", Assignments: []*accountsv1.Assignment{{InboundTag: "trojan", Credential: &accountsv1.Credential{Protocol: "trojan", Password: "changed"}}}},
			{Email: "unattempted", Assignments: []*accountsv1.Assignment{{InboundTag: "trojan", Credential: &accountsv1.Credential{Protocol: "trojan", Password: "new"}}}},
		}}))
	result, err := stream.CloseAndRecv()
	require(t, err)
	if result.GetOutcome() != "unknown" || result.GetRemoved() != 1 || result.GetAdded() != 0 || result.GetFailure() != "native_acknowledgement_unknown" {
		t.Fatalf("actual Xray rejection = %v", result)
	}
	assertNamed(t, f, "vless")
	assertNamed(t, f, "trojan", "move")
}
