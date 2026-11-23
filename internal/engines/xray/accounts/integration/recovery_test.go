package integration_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	accountsv1 "github.com/PapilioVPN/node/internal/engines/xray/accounts/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/xray/accounts/membership"
	"github.com/PapilioVPN/node/internal/engines/xray/accounts/native"
	"google.golang.org/grpc"
)

const executionPath = "/v1/xray/instances/primary/execution"

func TestAccountRestoreOnReplacementCore(t *testing.T) {
	f := startFixture(t, configuredAccount{Email: "configured", ID: credential})
	client := native.New(f.instance)
	service := membership.New(client)
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	scope := membership.Scope{InstanceID: instanceID, InboundTag: "vless"}
	result, err := service.SyncUsers(ctx, membership.SyncUsersRequest{Scope: scope, Accounts: []membership.AccountInput{
		{Email: "accepted", UUID: new(rotatedCredential)},
	}})
	require(t, err)
	if result.Outcome != membership.Confirmed {
		t.Fatalf("snapshot acceptance = %+v", result)
	}
	assertNamed(t, f, "vless", "accepted")
	require(t, service.Add(ctx, membership.AddRequest{Scope: scope, Email: "ephemeral", UUID: credential}))
	assertNamed(t, f, "vless", "accepted", "ephemeral")

	restart := func() *grpc.ClientConn {
		t.Helper()
		_, err := f.instance.Stop(ctx, instanceID)
		require(t, err)
		_, err = f.instance.Start(ctx, instanceID)
		require(t, err)
		conn, err := f.instance.NativeConnection(instanceID)
		require(t, err)
		return conn
	}
	commit, err := client.Initialize(ctx, instanceID, restart(), nil, nil)
	require(t, err)
	commit()
	assertNamed(t, f, "vless", "accepted")

	invalid := []membership.InboundSnapshot{{InboundTag: "vless", Accounts: []membership.AccountInput{{Email: "seed", UUID: new("invalid")}}}}
	if commit, err := client.Initialize(ctx, instanceID, restart(), invalid, nil); err == nil || commit != nil {
		t.Fatal("invalid initial snapshot produced a recovery commit")
	}
	commit, err = client.Initialize(ctx, instanceID, restart(), nil, nil)
	require(t, err)
	commit()
	assertNamed(t, f, "vless", "accepted")

	initial := []membership.InboundSnapshot{{InboundTag: "vless", Accounts: []membership.AccountInput{}}}
	commit, err = client.Initialize(ctx, instanceID, restart(), initial, nil)
	require(t, err)
	commit()
	assertNamed(t, f, "vless")
	commit, err = client.Initialize(ctx, instanceID, restart(), nil, nil)
	require(t, err)
	commit()
	assertNamed(t, f, "vless")
}

func (f fixture) hasManagedAccountInitializer() bool {
	type initializer = func(context.Context, string, *grpc.ClientConn, []membership.InboundSnapshot, []string) (func(), error)
	_, ok := any(f.instance).(interface{ SetAccountInitializer(initializer) })
	return ok
}

func TestSyncMembershipSurvivesManagedRestart(t *testing.T) {
	f := startFixture(t, configuredAccount{Email: "configured", ID: credential})
	if !f.hasManagedAccountInitializer() {
		t.Skip("requires the instances startup initializer")
	}
	first := &accountsv1.SyncUserRequest{Id: instanceID, InboundTags: []string{"vless", "trojan"}, Email: "member",
		Assignments: []*accountsv1.Assignment{
			{InboundTag: "vless", Credential: &accountsv1.Credential{Protocol: "vless", Uuid: rotatedCredential}},
			{InboundTag: "trojan", Credential: &accountsv1.Credential{Protocol: "trojan", Password: "retained"}},
		}}
	result, err := f.grpc.SyncUser(t.Context(), first)
	require(t, err)
	expectSync(t, result, 2, 0)
	f.request(t, http.MethodDelete, executionPath, nil, http.StatusOK)
	f.request(t, http.MethodPut, executionPath, nil, http.StatusOK)
	assertNamed(t, f, "vless", "configured", "member")
	assertNamed(t, f, "trojan", "member")

	f.request(t, http.MethodDelete, executionPath, nil, http.StatusOK)
	f.request(t, http.MethodPut, executionPath, map[string]any{
		"initial_users":    []map[string]any{{"inbound_tag": "vless", "accounts": []any{}}},
		"exclude_inbounds": []string{"trojan"},
	}, http.StatusOK)
	assertNamed(t, f, "vless")
	assertNamed(t, f, "trojan")
	f.request(t, http.MethodDelete, executionPath, nil, http.StatusOK)
	f.request(t, http.MethodPut, executionPath, nil, http.StatusOK)
	assertNamed(t, f, "vless")
	assertNamed(t, f, "trojan")
}
