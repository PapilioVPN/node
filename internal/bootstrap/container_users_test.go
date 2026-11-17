//go:build container

package bootstrap_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/xtls/xray-core/proxy/trojan"
	"google.golang.org/protobuf/proto"

	userv1 "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/grpcapi/proto/v1"
	xrayv1 "github.com/PapilioVPN/node/internal/bootstrap/xrayapi/proto/v1"
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
	verifyContainerUserAdditions(t, ctx, client)
	verifyContainerUserDelta(t, ctx, client)
}

func verifyContainerUserAdditions(t *testing.T, ctx context.Context, client xrayv1.XrayServiceClient) {
	t.Helper()
	request := &userv1.AddInboundUsersRequest{InstanceId: "packaged-xray", InboundTag: "users", Users: []*userv1.InboundUser{
		{Email: "packaged-collection-a@local", Account: &userv1.InboundUser_Trojan{Trojan: &userv1.PasswordAccount{Password: "test-only-collection-a"}}},
		{Email: "packaged-collection-b@local", Account: &userv1.InboundUser_Trojan{Trojan: &userv1.PasswordAccount{Password: "test-only-collection-b"}}},
	}}
	result, err := client.AddInboundUsers(ctx, request)
	if err != nil || len(result.Items) != 2 {
		t.Fatalf("packaged user collection failed: %v", err)
	}
	for i, item := range result.Items {
		if item.State != userv1.UserAdditionState_USER_ADDITION_STATE_ACKNOWLEDGED || item.Email != request.Users[i].Email || item.AcknowledgedAtUnixMs == nil {
			t.Fatal("packaged collection acknowledgement changed")
		}
		stored, err := client.GetInboundUserMetadata(ctx, &userv1.GetInboundUserMetadataRequest{InstanceId: "packaged-xray", InboundTag: "users", Email: item.Email})
		if err != nil || stored.User == nil || stored.User.Email != item.Email {
			t.Fatal("packaged collection did not change actual membership")
		}
		if _, err := client.RemoveInboundUser(ctx, &userv1.RemoveInboundUserRequest{InstanceId: "packaged-xray", InboundTag: "users", Email: item.Email}); err != nil {
			t.Fatal("packaged collection cleanup failed")
		}
	}
}

// The packaged executable must expose the same native delta, not only compile it.
func verifyContainerUserDelta(t *testing.T, ctx context.Context, client xrayv1.XrayServiceClient) {
	t.Helper()
	user := &userv1.InboundUser{Email: "packaged-delta@local", Account: &userv1.InboundUser_Trojan{Trojan: &userv1.PasswordAccount{Password: "test-only-delta-first"}}}
	if _, err := client.AddInboundUser(ctx, &userv1.AddInboundUserRequest{InstanceId: "packaged-xray", InboundTag: "users", User: user}); err != nil {
		t.Fatal("packaged delta seed failed")
	}
	user.Level = 31
	user.GetTrojan().Password = "test-only-delta-next"
	result, err := client.ApplyInboundUserDelta(ctx, &userv1.ApplyInboundUserDeltaRequest{InstanceId: "packaged-xray", InboundTag: "users", Users: []*userv1.InboundUser{user}, RemoveEmails: []string{"missing-packaged-delta"}})
	if err != nil || len(result.Steps) != 3 || result.ObservedAtUnixMs == nil || result.Steps[0].State != userv1.UserDeltaState_USER_DELTA_STATE_NOT_REQUIRED || result.Steps[1].State != userv1.UserDeltaState_USER_DELTA_STATE_ACKNOWLEDGED || result.Steps[2].State != userv1.UserDeltaState_USER_DELTA_STATE_ACKNOWLEDGED {
		t.Fatalf("packaged user delta failed: %v", err)
	}
	stored, err := client.GetInboundUser(ctx, &userv1.GetInboundUserRequest{InstanceId: "packaged-xray", InboundTag: "users", Email: user.Email})
	if err != nil || stored.User == nil || stored.User.Level != 31 || stored.User.Account == nil {
		t.Fatal("packaged delta lost actual native membership/level")
	}
	var account trojan.Account
	if err := proto.Unmarshal(stored.User.Account.Protobuf, &account); err != nil || account.Password != user.GetTrojan().Password {
		t.Fatal("packaged delta did not replace actual native credentials; withheld")
	}
	cleanup, err := client.ApplyInboundUserDelta(ctx, &userv1.ApplyInboundUserDeltaRequest{InstanceId: "packaged-xray", InboundTag: "users", RemoveEmails: []string{user.Email}})
	if err != nil || len(cleanup.Steps) != 1 || cleanup.Steps[0].State != userv1.UserDeltaState_USER_DELTA_STATE_ACKNOWLEDGED {
		t.Fatal("packaged delta cleanup failed")
	}
	count, err := client.GetInboundUserCount(ctx, &userv1.GetInboundUserCountRequest{InstanceId: "packaged-xray", InboundTag: "users"})
	if err != nil || count.Count != 1 {
		t.Fatal("packaged delta changed the baseline user")
	}
}
