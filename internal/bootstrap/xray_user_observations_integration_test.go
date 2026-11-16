//go:build integration

package bootstrap_test

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"testing"

	"go.uber.org/fx"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/bootstrap"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
	xrayv1 "github.com/PapilioVPN/node/internal/modules/engines/xray/grpcapi/proto/v1"
	rpc "github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/shared/testutil"
)

func TestUserObservationGrantsStayIndependentOverMTLS(t *testing.T) {
	for _, metadata := range []bool{false, true} {
		for _, accounts := range []bool{false, true} {
			t.Run(fmt.Sprintf("metadata=%t/accounts=%t", metadata, accounts), func(t *testing.T) {
				certificates := testutil.NewCertificates(t)
				cfg := certificates.Config()
				p := &cfg.Communications.GRPC.Permissions
				p.ReadEngineUserMetadata, p.ReadEngineUserAccounts = metadata, accounts
				prior := !metadata && !accounts
				p.ReadInstanceStatus, p.ControlInstances, p.ValidateInstanceConfig = prior, prior, prior
				p.ReadEngineInfo, p.ReadEngineStats, p.ResetEngineStats = prior, prior, prior
				p.ManageEngineUsers, p.ReadEngineUsers, p.ReadEngineOnline = prior, prior, prior
				p.ReadEngineRouting, p.ReadEngineOutbounds, p.TestEngineRouting, p.ManageEngineRouting = prior, prior, prior, prior
				var server *rpc.Server
				app := bootstrap.New(cfg, "test", fx.NopLogger, fx.Populate(&server))
				if err := app.Start(t.Context()); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := app.Stop(context.Background()); err != nil {
						t.Error(err)
					}
					_ = server.Close()
				})
				client := xrayClient(t, server, certificates)
				_, e1 := client.GetInboundUserMetadata(t.Context(), &xrayv1.GetInboundUserMetadataRequest{InstanceId: "missing", InboundTag: "in", Email: "user"})
				_, e2 := client.ListInboundUserMetadata(t.Context(), &xrayv1.ListInboundUserMetadataRequest{InstanceId: "missing", InboundTag: "in"})
				_, e3 := client.GetInboundUser(t.Context(), &xrayv1.GetInboundUserRequest{InstanceId: "missing", InboundTag: "in", Email: "user"})
				_, e4 := client.ListInboundUsers(t.Context(), &xrayv1.ListInboundUsersRequest{InstanceId: "missing", InboundTag: "in"})
				for i, err := range []error{e1, e2, e3, e4} {
					allowed := metadata
					if i >= 2 {
						allowed = accounts
					}
					want := codes.PermissionDenied
					if allowed {
						want = codes.NotFound
					}
					if status.Code(err) != want {
						t.Fatalf("operation %d expanded another grant or was not bound: %v", i, err)
					}
				}
				if !prior {
					_, err := client.GetInboundUserCount(t.Context(), &xrayv1.GetInboundUserCountRequest{InstanceId: "missing", InboundTag: "in"})
					if status.Code(err) != codes.PermissionDenied {
						t.Fatal("user views enabled counts")
					}
					_, err = client.RemoveInboundUser(t.Context(), &xrayv1.RemoveInboundUserRequest{InstanceId: "missing", InboundTag: "in", Email: "user"})
					if status.Code(err) != codes.PermissionDenied {
						t.Fatal("user views enabled mutations")
					}
				}
			})
		}
	}
}

func verifySingleUserViews(t *testing.T, ctx context.Context, client xrayv1.XrayServiceClient, id, tag, email string, want *domain.ObservedInboundUser) {
	t.Helper()
	metadata, err := client.GetInboundUserMetadata(ctx, &xrayv1.GetInboundUserMetadataRequest{InstanceId: id, InboundTag: tag, Email: email})
	if err != nil || metadata.InstanceId != id || metadata.InboundTag != tag || metadata.Email != email || metadata.ObservedAtUnixMs <= 0 || (metadata.User != nil) != (want != nil) {
		t.Fatalf("metadata single lost exact selection/absence/time: %v", err)
	}
	full, err := client.GetInboundUser(ctx, &xrayv1.GetInboundUserRequest{InstanceId: id, InboundTag: tag, Email: email})
	if err != nil || full.InstanceId != id || full.InboundTag != tag || full.Email != email || full.ObservedAtUnixMs <= 0 || (full.User != nil) != (want != nil) {
		t.Fatalf("full single lost exact selection/absence/time: %v", err)
	}
	if want == nil {
		return
	}
	m, r := metadata.User, full.User
	if m.Email != want.Email || m.Level != want.Level || m.AccountType != want.Account.TypeName || r.Email != want.Email || r.Level != want.Level || r.Account == nil || r.Account.TypeName != want.Account.TypeName || !bytes.Equal(r.Account.Protobuf, want.Account.Protobuf) {
		t.Fatal("public single changed native user identity/account; credentials withheld")
	}
}

func verifyUserListViews(t *testing.T, ctx context.Context, client xrayv1.XrayServiceClient, want domain.InstanceInboundUsersObservation) {
	t.Helper()
	id, tag := string(want.InstanceID), want.InboundTag
	metadata, err := client.ListInboundUserMetadata(ctx, &xrayv1.ListInboundUserMetadataRequest{InstanceId: id, InboundTag: tag})
	if err != nil || metadata.InstanceId != id || metadata.InboundTag != tag || metadata.ObservedAtUnixMs <= 0 || len(metadata.Users) != len(want.Observation.Users) {
		t.Fatalf("metadata list lost native membership/time: %v", err)
	}
	full, err := client.ListInboundUsers(ctx, &xrayv1.ListInboundUsersRequest{InstanceId: id, InboundTag: tag})
	if err != nil || full.InstanceId != id || full.InboundTag != tag || full.ObservedAtUnixMs <= 0 || len(full.Users) != len(want.Observation.Users) {
		t.Fatalf("full list lost native membership/time: %v", err)
	}
	type key struct {
		email         string
		level         uint32
		kind, payload string
	}
	expected, actual := map[key]int{}, map[key]int{}
	expectedMetadata, actualMetadata := map[key]int{}, map[key]int{}
	for _, r := range want.Observation.Users {
		expected[key{r.Email, r.Level, r.Account.TypeName, string(r.Account.Protobuf)}]++
		expectedMetadata[key{email: r.Email, level: r.Level, kind: r.Account.TypeName}]++
	}
	var previous *xrayv1.InboundUserRecord
	for _, r := range full.Users {
		if r.Account == nil {
			t.Fatal("public full list dropped account presence")
		}
		if previous != nil && (previous.Email > r.Email || previous.Email == r.Email && (previous.Level > r.Level || previous.Level == r.Level && previous.Account.TypeName > r.Account.TypeName)) {
			t.Fatal("public full list lost application ordering")
		}
		previous = r
		actual[key{r.Email, r.Level, r.Account.TypeName, string(r.Account.Protobuf)}]++
	}
	for i, r := range metadata.Users {
		if i > 0 {
			p := metadata.Users[i-1]
			if p.Email > r.Email || p.Email == r.Email && (p.Level > r.Level || p.Level == r.Level && p.AccountType > r.AccountType) {
				t.Fatal("public metadata list lost application ordering")
			}
		}
		actualMetadata[key{email: r.Email, level: r.Level, kind: r.AccountType}]++
	}
	if !maps.Equal(expected, actual) || !maps.Equal(expectedMetadata, actualMetadata) {
		t.Fatal("public list lost native duplicates/anonymous users/account bytes; credentials withheld")
	}
}

func verifyFailedUserViews(t *testing.T, ctx context.Context, client xrayv1.XrayServiceClient, id, tag string, want codes.Code) {
	t.Helper()
	r1, e1 := client.GetInboundUserMetadata(ctx, &xrayv1.GetInboundUserMetadataRequest{InstanceId: id, InboundTag: tag, Email: "user"})
	r2, e2 := client.ListInboundUserMetadata(ctx, &xrayv1.ListInboundUserMetadataRequest{InstanceId: id, InboundTag: tag})
	r3, e3 := client.GetInboundUser(ctx, &xrayv1.GetInboundUserRequest{InstanceId: id, InboundTag: tag, Email: "user"})
	r4, e4 := client.ListInboundUsers(ctx, &xrayv1.ListInboundUsersRequest{InstanceId: id, InboundTag: tag})
	if r1 != nil || r2 != nil || r3 != nil || r4 != nil {
		t.Fatal("failed user views returned successful partial facts")
	}
	for _, err := range []error{e1, e2, e3, e4} {
		if status.Code(err) != want {
			t.Fatalf("failed user observation code changed: %v; want %v", err, want)
		}
	}
}
