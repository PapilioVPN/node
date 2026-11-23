package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	accountsv1 "github.com/PapilioVPN/node/internal/engines/xray/accounts/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/xray/accounts/membership"
	"google.golang.org/grpc/codes"
)

func TestSyncUsersHTTP(t *testing.T) {
	f := startFixture(t)
	target := trafficTarget(t)
	f.request(t, http.MethodPost, accountsPath, map[string]string{"email": email, "uuid": credential}, http.StatusNoContent)
	f.request(t, http.MethodPut, accountsPath, map[string]any{}, http.StatusBadRequest)
	f.request(t, http.MethodPut, accountsPath, map[string]any{"accounts": nil}, http.StatusBadRequest)
	f.request(t, http.MethodPut, accountsPath, map[string]any{"accounts": []map[string]string{
		{"email": "new", "uuid": rotatedCredential},
		{"email": "NEW", "uuid": credential},
	}}, http.StatusBadRequest)
	f.checkCount(t, 1)
	checkTraffic(t, f.inboundPort, target, true)

	body := map[string]any{"accounts": []map[string]string{{"email": "replacement", "uuid": rotatedCredential}}}
	raw := f.request(t, http.MethodPut, accountsPath, body, http.StatusOK)
	var out membership.SyncResult
	require(t, json.Unmarshal(raw, &out))
	if out.Outcome != membership.Confirmed || out.Added != 1 || out.Removed != 1 {
		t.Fatalf("unexpected membership outcome: %+v", out)
	}
	f.checkCount(t, 1)
	checkTraffic(t, f.inboundPort, target, false)
	checkTrafficCredential(t, f.inboundPort, target, rotatedCredential, true)
	f.request(t, http.MethodPut, accountsPath, map[string]any{"accounts": []any{}}, http.StatusOK)
	f.checkCount(t, 0)
	checkTrafficCredential(t, f.inboundPort, target, rotatedCredential, false)
}

func TestSyncUsersGRPC(t *testing.T) {
	f := startFixture(t)
	target := trafficTarget(t)
	ctx := t.Context()
	req := &accountsv1.SyncUsersRequest{Id: instanceID, InboundTag: "vless"}
	_, err := f.grpc.SyncUsers(ctx, req)
	checkRPCError(t, err, codes.InvalidArgument)
	for _, size := range []int{100, 1000} {
		snapshot := []*accountsv1.VLESSAccount{
			{Email: email, Uuid: ptr(credential)},
		}
		for i := 1; i < size; i++ {
			snapshot = append(snapshot, &accountsv1.VLESSAccount{
				Email: fmt.Sprintf("fixture-%d", i), Uuid: ptr(fmt.Sprintf("62f0622c-a8d5-4552-8b71-%012x", i)),
			})
		}
		req.Snapshot = &accountsv1.VLESSAccounts{Accounts: snapshot}
		result, err := f.grpc.SyncUsers(ctx, req)
		require(t, err)
		checkSyncResult(t, result, uint32(size), 0)
		f.checkCount(t, int64(size))
		checkTraffic(t, f.inboundPort, target, true)
		result, err = f.grpc.SyncUsers(ctx, req)
		require(t, err)
		checkSyncResult(t, result, 0, 0)
		req.Snapshot = &accountsv1.VLESSAccounts{}
		result, err = f.grpc.SyncUsers(ctx, req)
		require(t, err)
		checkSyncResult(t, result, 0, uint32(size))
		f.checkCount(t, 0)
		checkTraffic(t, f.inboundPort, target, false)
	}
}

func checkSyncResult(t *testing.T, result *accountsv1.SyncResult, added, removed uint32) {
	t.Helper()
	if result.GetOutcome() != "confirmed" || result.GetAdded() != added || result.GetRemoved() != removed || result.GetFailure() != "" {
		t.Fatal("unexpected sync outcome or effects")
	}
}

func TestSyncUsersCanceledDuringReplacement(t *testing.T) {
	f := startFixture(t)
	target := trafficTarget(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	service := f.interceptEdits(t, func(count int32) error {
		if count == 3 {
			cancel()
		}
		return nil
	})
	scope := membership.Scope{InstanceID: instanceID, InboundTag: "vless"}
	for _, account := range []membership.AddRequest{
		{Scope: scope, Email: "first", UUID: credential},
		{Scope: scope, Email: "second", UUID: rotatedCredential},
	} {
		require(t, service.Add(t.Context(), account))
	}
	result, err := service.SyncUsers(ctx, membership.SyncUsersRequest{Scope: scope, Accounts: []membership.AccountInput{}})
	require(t, err)
	if result.Outcome != membership.Partial || result.Removed != 1 || result.Added != 0 || result.Failure != "canceled" {
		t.Fatalf("unexpected cancellation result: %+v", result)
	}
	f.checkCount(t, 1)
	checkTraffic(t, f.inboundPort, target, false)
	checkTrafficCredential(t, f.inboundPort, target, rotatedCredential, true)
}

func TestSyncUsersPreservesAnonymousAndRejectsCollisionsBeforeRemoval(t *testing.T) {
	f := startFixture(t, configuredAccount{ID: rotatedCredential}, configuredAccount{Email: email, ID: credential})
	target := trafficTarget(t)
	f.checkCount(t, 1)
	checkTrafficCredential(t, f.inboundPort, target, rotatedCredential, true)

	req := &accountsv1.SyncUsersRequest{Id: instanceID, InboundTag: "vless", Snapshot: &accountsv1.VLESSAccounts{
		Accounts: []*accountsv1.VLESSAccount{{Email: "new", Uuid: ptr(rotatedCredential)}},
	}}
	conflict, err := f.grpc.SyncUsers(t.Context(), req)
	require(t, err)
	if conflict.GetOutcome() != "not_applied" || conflict.GetFailure() != "conflict" ||
		conflict.GetAdded() != 0 || conflict.GetRemoved() != 0 {
		t.Fatal("collision did not reject the whole plan")
	}
	f.checkCount(t, 1)
	checkTraffic(t, f.inboundPort, target, true)

	req.Snapshot.Accounts = nil
	result, err := f.grpc.SyncUsers(t.Context(), req)
	require(t, err)
	checkSyncResult(t, result, 0, 1)
	f.checkCount(t, 0)
	checkTraffic(t, f.inboundPort, target, false)
	checkTrafficCredential(t, f.inboundPort, target, rotatedCredential, true)
}
