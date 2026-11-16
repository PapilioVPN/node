//go:build container

package bootstrap_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/xtls/xray-core/proxy/trojan"
	"google.golang.org/protobuf/proto"

	xrayv1 "github.com/PapilioVPN/node/internal/modules/xray/grpcapi/proto/v1"
	userv1 "github.com/PapilioVPN/node/internal/modules/xray/users/grpcapi/proto/v1"
)

func verifyContainerUserViews(t *testing.T, ctx context.Context, client xrayv1.XrayServiceClient) {
	t.Helper()
	metadata, err := client.GetInboundUserMetadata(ctx, &userv1.GetInboundUserMetadataRequest{InstanceId: "packaged-xray", InboundTag: "users", Email: "packaged@local"})
	if err != nil || metadata.User == nil || metadata.User.Email != "packaged@local" || metadata.User.Level != 23 || metadata.User.AccountType != "xray.proxy.trojan.Account" || metadata.ObservedAtUnixMs <= 0 {
		t.Fatalf("packaged native user metadata failed: %v", err)
	}
	encoded, err := proto.Marshal(metadata)
	if err != nil || bytes.Contains(encoded, []byte("test-only-packaged-account")) {
		t.Fatal("packaged metadata exposed credentials")
	}
	full, err := client.GetInboundUser(ctx, &userv1.GetInboundUserRequest{InstanceId: "packaged-xray", InboundTag: "users", Email: "packaged@local"})
	if err != nil || full.User == nil || full.User.Account == nil || full.User.Account.TypeName != metadata.User.AccountType || full.User.Email != metadata.User.Email || full.User.Level != metadata.User.Level || full.ObservedAtUnixMs <= 0 {
		t.Fatalf("packaged native user record failed: %v", err)
	}
	var account trojan.Account
	if err := proto.Unmarshal(full.User.Account.Protobuf, &account); err != nil || account.Password != "test-only-packaged-account" {
		t.Fatal("packaged account payload lost actual native credentials; withheld")
	}
	list, err := client.ListInboundUserMetadata(ctx, &userv1.ListInboundUserMetadataRequest{InstanceId: "packaged-xray", InboundTag: "users"})
	if err != nil || len(list.Users) != 1 || !proto.Equal(list.Users[0], metadata.User) || list.ObservedAtUnixMs <= 0 {
		t.Fatalf("packaged metadata collection failed: %v", err)
	}
	records, err := client.ListInboundUsers(ctx, &userv1.ListInboundUsersRequest{InstanceId: "packaged-xray", InboundTag: "users"})
	if err != nil || len(records.Users) != 1 || !proto.Equal(records.Users[0], full.User) || records.ObservedAtUnixMs <= 0 {
		t.Fatalf("packaged full collection failed: %v", err)
	}
	absent, err := client.GetInboundUser(ctx, &userv1.GetInboundUserRequest{InstanceId: "packaged-xray", InboundTag: "users", Email: "missing"})
	if err != nil || absent.User != nil || absent.ObservedAtUnixMs <= 0 {
		t.Fatalf("packaged missing user became a fabricated account: %v", err)
	}
}
