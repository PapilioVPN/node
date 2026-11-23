package integration_test

import (
	"context"
	"testing"
	"time"

	accountsv1 "github.com/PapilioVPN/node/internal/engines/xray/accounts/bridge/grpc/proto/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestSyncUserStreamAcceptsSeveralUsers(t *testing.T) {
	f := startFixture(t, configuredAccount{Email: "before", ID: credential})
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	stream, err := f.grpc.SyncUserStream(ctx)
	require(t, err)
	require(t, stream.Send(&accountsv1.SyncUserRequest{Id: instanceID, InboundTag: "vless", Email: "before"}))
	require(t, stream.Send(&accountsv1.SyncUserRequest{Id: instanceID, InboundTag: "vless", Email: "after", Uuid: new(rotatedCredential)}))
	result, err := stream.CloseAndRecv()
	require(t, err)
	expectSync(t, result, 1, 1)
	assertNamed(t, f, "vless", "after")
}

func TestSyncUserStreamRejectsInterruptedOrMixedScope(t *testing.T) {
	f := startFixture(t, configuredAccount{Email: "before", ID: credential})
	first := &accountsv1.SyncUserRequest{Id: instanceID, InboundTag: "vless", Email: "before"}
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()

	mixed, err := f.grpc.SyncUserStream(ctx)
	require(t, err)
	require(t, mixed.Send(first))
	require(t, mixed.Send(&accountsv1.SyncUserRequest{Id: instanceID, InboundTag: "trojan", Email: "another"}))
	if _, err := mixed.CloseAndRecv(); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("mixed scope = %v", err)
	}
	assertNamed(t, f, "vless", "before")

	interruptedCtx, interrupt := context.WithTimeout(ctx, time.Second)
	defer interrupt()
	interrupted, err := f.grpc.SyncUserStream(interruptedCtx)
	require(t, err)
	require(t, interrupted.Send(first))
	interrupt()
	if _, err := interrupted.CloseAndRecv(); status.Code(err) != codes.Canceled {
		t.Fatalf("interrupted stream = %v", err)
	}
	assertNamed(t, f, "vless", "before")
}
