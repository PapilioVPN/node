package integration_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	accountsv1 "github.com/PapilioVPN/node/internal/engines/xray/accounts/bridge/grpc/proto/v1"
)

func TestLiveSyncPreservesExcludedInbound(t *testing.T) {
	const manual = "manual"
	const addedManual = "manual-live"
	f := startFixture(t, configuredAccount{Email: manual, ID: credential})
	targetPort := trafficTarget(t)
	startExcluded := func() {
		t.Helper()
		f.request(t, http.MethodDelete, executionPath, nil, http.StatusOK)
		f.request(t, http.MethodPut, executionPath, map[string]any{"exclude_inbounds": []string{"vless"}}, http.StatusOK)
	}
	startExcluded()
	assertNamed(t, f, "vless", manual)
	checkTraffic(t, f.inboundPort, targetPort, true)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	_, err := f.grpc.AddVLESSAccount(ctx, &accountsv1.AddVLESSAccountRequest{
		Id: instanceID, InboundTag: "vless", Email: addedManual, Uuid: rotatedCredential,
	})
	require(t, err)
	assertNamed(t, f, "vless", manual, addedManual)

	// The legacy selector can explicitly target an excluded inbound.
	result, err := f.grpc.SyncUser(ctx, &accountsv1.SyncUserRequest{Id: instanceID, InboundTag: "vless", Email: manual})
	require(t, err)
	expectSync(t, result, 0, 0)
	f.request(t, http.MethodPut, accountsPath, map[string]any{"accounts": []any{}}, http.StatusOK)
	assertNamed(t, f, "vless", manual, addedManual)

	tags := []string{"vless", "trojan"}
	inboundAssignments := func(password string) []*accountsv1.Assignment {
		return []*accountsv1.Assignment{
			{InboundTag: "vless", Credential: &accountsv1.Credential{Protocol: "vless", Uuid: rotatedCredential}},
			{InboundTag: "trojan", Credential: &accountsv1.Credential{Protocol: "trojan", Password: password}},
		}
	}
	result, err = f.grpc.SyncUsers(ctx, &accountsv1.SyncUsersRequest{Id: instanceID, InboundTags: tags,
		Snapshot: &accountsv1.VLESSAccounts{Accounts: []*accountsv1.VLESSAccount{{Email: manual, Assignments: inboundAssignments("managed")}}}})
	require(t, err)
	expectSync(t, result, 1, 0)
	assertNamed(t, f, "vless", manual, addedManual)
	assertNamed(t, f, "trojan", manual)

	chunked, err := f.grpc.SyncUsersChunked(ctx)
	require(t, err)
	require(t, chunked.Send(&accountsv1.UsersChunk{Id: instanceID, InboundTags: tags, Last: true,
		Accounts: []*accountsv1.VLESSAccount{{Email: manual}}}))
	result, err = chunked.CloseAndRecv()
	require(t, err)
	expectSync(t, result, 0, 1)
	assertNamed(t, f, "vless", manual, addedManual)
	assertNamed(t, f, "trojan")

	stream, err := f.grpc.SyncUserStream(ctx)
	require(t, err)
	require(t, stream.Send(&accountsv1.SyncUserRequest{Id: instanceID, InboundTags: tags, Email: manual, Assignments: inboundAssignments("streamed")}))
	result, err = stream.CloseAndRecv()
	require(t, err)
	expectSync(t, result, 1, 0)
	assertNamed(t, f, "vless", manual, addedManual)
	assertNamed(t, f, "trojan", manual)
	checkTraffic(t, f.inboundPort, targetPort, true)
	checkTrafficCredential(t, f.inboundPort, targetPort, rotatedCredential, true)

	startExcluded()
	assertNamed(t, f, "vless", manual)
	assertNamed(t, f, "trojan", manual)
	checkTraffic(t, f.inboundPort, targetPort, true)
}
