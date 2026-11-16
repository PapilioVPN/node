package mutations_test

import (
	"bytes"
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	grpcapi "github.com/PapilioVPN/node/internal/bootstrap/xrayapi"
	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	infogrpc "github.com/PapilioVPN/node/internal/modules/xray/info/grpcapi"
	trafficgrpc "github.com/PapilioVPN/node/internal/modules/xray/traffic/grpcapi"
	"github.com/PapilioVPN/node/internal/modules/xray/users/domain"
	usergrpc "github.com/PapilioVPN/node/internal/modules/xray/users/grpcapi"
	"github.com/PapilioVPN/node/internal/modules/xray/users/grpcapi/mutations"
	"github.com/PapilioVPN/node/internal/modules/xray/users/grpcapi/online"
	userv1 "github.com/PapilioVPN/node/internal/modules/xray/users/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/shared/testutil"
)

type userAdder func(context.Context, instances.InstanceID, domain.InboundUserAddition) (domain.InstanceInboundUserMutation, error)

func (f userAdder) AddInboundUser(ctx context.Context, id instances.InstanceID, addition domain.InboundUserAddition) (domain.InstanceInboundUserMutation, error) {
	return f(ctx, id, addition)
}

func TestUserAdditionChecksIdentityAndIndependentGrantBeforeShape(t *testing.T) {
	if _, err := (grpcapi.Handler{UserAPI: usergrpc.UserAPI{MutationAPI: mutations.MutationAPI{AllowUserManagement: true}}}).AddInboundUser(t.Context(), nil); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("unverified caller reached addition: %v", err)
	}
	if _, err := (grpcapi.Handler{TrafficAPI: trafficgrpc.TrafficAPI{AllowStatsRead: true, AllowStatsReset: true}, InfoAPI: infogrpc.InfoAPI{AllowInfoRead: true}, UserAPI: usergrpc.UserAPI{OnlineAPI: online.OnlineAPI{AllowOnlineRead: true}}}).AddInboundUser(testutil.VerifiedGRPCContext(t), nil); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("other grants enabled addition: %v", err)
	}
}

func TestUserAdditionDecodesEveryTypedAccountAndRetainsLateAcknowledgement(t *testing.T) {
	key, psk := bytes.Repeat([]byte{7}, 32), bytes.Repeat([]byte{8}, 32)
	zero := uint32(0)
	for _, tc := range []struct {
		wire *userv1.InboundUser
		want domain.UserAccount
	}{
		{&userv1.InboundUser{Account: &userv1.InboundUser_Vless{Vless: &userv1.VLESSAccount{Id: "native-id", Flow: "xtls-rprx-vision"}}}, domain.UserAccount{Protocol: domain.VLESS, VLESS: &domain.VLESSAccount{ID: "native-id", Flow: "xtls-rprx-vision"}}},
		{&userv1.InboundUser{Account: &userv1.InboundUser_Vmess{Vmess: &userv1.VMessAccount{Id: "native-id"}}}, domain.UserAccount{Protocol: domain.VMess, VMess: &domain.VMessAccount{ID: "native-id"}}},
		{&userv1.InboundUser{Account: &userv1.InboundUser_Trojan{Trojan: &userv1.PasswordAccount{Password: " password "}}}, domain.UserAccount{Protocol: domain.Trojan, Trojan: &domain.PasswordAccount{Password: " password "}}},
		{&userv1.InboundUser{Account: &userv1.InboundUser_Shadowsocks{Shadowsocks: &userv1.ShadowsocksAccount{Password: " password ", Cipher: userv1.ShadowsocksCipher_SHADOWSOCKS_CIPHER_XCHACHA20_POLY1305, IvCheck: true}}}, domain.UserAccount{Protocol: domain.Shadowsocks, Shadowsocks: &domain.ShadowsocksAccount{Password: " password ", Cipher: domain.XChaCha20Poly1305, IVCheck: true}}},
		{&userv1.InboundUser{Account: &userv1.InboundUser_Shadowsocks_2022{Shadowsocks_2022: &userv1.Shadowsocks2022Account{Key: key}}}, domain.UserAccount{Protocol: domain.Shadowsocks2022, Shadowsocks2022: &domain.Shadowsocks2022Account{Key: key}}},
		{&userv1.InboundUser{Account: &userv1.InboundUser_Hysteria{Hysteria: &userv1.HysteriaAccount{Auth: " auth "}}}, domain.UserAccount{Protocol: domain.Hysteria, Hysteria: &domain.HysteriaAccount{Auth: " auth "}}},
		{&userv1.InboundUser{Account: &userv1.InboundUser_Masque{Masque: &userv1.PasswordAccount{Password: " password "}}}, domain.UserAccount{Protocol: domain.MASQUE, MASQUE: &domain.PasswordAccount{Password: " password "}}},
		{&userv1.InboundUser{Account: &userv1.InboundUser_Wireguard{Wireguard: &userv1.WireGuardAccount{PublicKey: key, PreSharedKey: psk, AllowedIps: []string{"10.0.0.2/32", "fd00::2/128"}, KeepAliveSeconds: &zero}}}, domain.UserAccount{Protocol: domain.WireGuard, WireGuard: &domain.WireGuardAccount{PublicKey: key, PreSharedKey: psk, AllowedIPs: []string{"10.0.0.2/32", "fd00::2/128"}, KeepAliveSeconds: &zero}}},
		{&userv1.InboundUser{Account: &userv1.InboundUser_Wireguard{Wireguard: &userv1.WireGuardAccount{PublicKey: key, AllowedIps: []string{"10.0.0.2/32"}}}}, domain.UserAccount{Protocol: domain.WireGuard, WireGuard: &domain.WireGuardAccount{PublicKey: key, AllowedIPs: []string{"10.0.0.2/32"}}}},
	} {
		t.Run(string(tc.want.Protocol), func(t *testing.T) {
			ctx, cancel := context.WithCancel(testutil.VerifiedGRPCContext(t))
			defer cancel()
			tc.wire.Email, tc.wire.Level = " Mixed@Example ", math.MaxUint32
			ack := time.UnixMilli(1700000000123)
			calls := 0
			h := grpcapi.Handler{UserAPI: usergrpc.UserAPI{MutationAPI: mutations.MutationAPI{AllowUserManagement: true, UserAddition: userAdder(func(received context.Context, id instances.InstanceID, addition domain.InboundUserAddition) (domain.InstanceInboundUserMutation, error) {
				calls++
				if received != ctx || id != " edge " || addition.InboundTag != " exact>>>tag " || addition.User.Email != tc.wire.Email || addition.User.Level != math.MaxUint32 || !reflect.DeepEqual(addition.User.Account, tc.want) {
					t.Fatal("typed identity/account/context changed")
				}
				cancel()
				return domain.InstanceInboundUserMutation{InstanceID: id, Selection: domain.InboundUserSelection{InboundTag: addition.InboundTag, Email: addition.User.Email}, Acknowledgement: domain.UserMutationAcknowledgement{AcknowledgedAt: ack}}, nil
			})}}}
			response, err := h.AddInboundUser(ctx, &userv1.AddInboundUserRequest{InstanceId: " edge ", InboundTag: " exact>>>tag ", User: tc.wire})
			if err != nil || calls != 1 || response.InstanceId != " edge " || response.InboundTag != " exact>>>tag " || response.Email != tc.wire.Email || response.AcknowledgedAtUnixMs != ack.UnixMilli() {
				t.Fatalf("confirmed addition was lost/replayed: %v", err)
			}
			batchCtx, batchCancel := context.WithCancel(testutil.VerifiedGRPCContext(t))
			defer batchCancel()
			batchCalls := 0
			batch := usergrpc.UserAPI{MutationAPI: mutations.MutationAPI{AllowUserManagement: true, UserAdditions: usersAdder(func(received context.Context, id instances.InstanceID, input domain.InboundUserAdditions) (domain.InstanceInboundUserAdditions, error) {
				batchCalls++
				if received != batchCtx || id != " edge " || input.InboundTag != " exact>>>tag " || len(input.Users) != 1 || input.Users[0].Email != tc.wire.Email || input.Users[0].Level != math.MaxUint32 || !reflect.DeepEqual(input.Users[0].Account, tc.want) {
					t.Fatal("typed collection account/context changed")
				}
				batchCancel()
				return domain.InstanceInboundUserAdditions{InstanceID: id, InboundTag: input.InboundTag, Report: domain.UserAdditionsReport{Items: []domain.UserAdditionResult{{Email: input.Users[0].Email, State: domain.UserMutationAcknowledged, AcknowledgedAt: ack}}}}, nil
			})}}
			got, err := batch.AddInboundUsers(batchCtx, &userv1.AddInboundUsersRequest{InstanceId: " edge ", InboundTag: " exact>>>tag ", Users: []*userv1.InboundUser{tc.wire}})
			if err != nil || batchCalls != 1 || len(got.Items) != 1 || got.Items[0].Email != tc.wire.Email || got.Items[0].GetAcknowledgedAtUnixMs() != ack.UnixMilli() {
				t.Fatal("typed collection acknowledgement lost")
			}

		})
	}
}

func TestUserAdditionRejectsMissingOrMalformedAccountsBeforeApplication(t *testing.T) {
	h := grpcapi.Handler{UserAPI: usergrpc.UserAPI{MutationAPI: mutations.MutationAPI{AllowUserManagement: true}}} // A call into the application would panic.
	for _, user := range []*userv1.InboundUser{
		nil, {}, {Email: "user"}, {Email: "user", Account: &userv1.InboundUser_Vless{}},
		{Email: "user", Account: &userv1.InboundUser_Trojan{Trojan: &userv1.PasswordAccount{}}},
		{Email: "user", Account: &userv1.InboundUser_Shadowsocks{Shadowsocks: &userv1.ShadowsocksAccount{Password: "test-only", Cipher: 100}}},
		{Email: "user", Account: &userv1.InboundUser_Shadowsocks_2022{Shadowsocks_2022: &userv1.Shadowsocks2022Account{Key: []byte{7}}}},
		{Email: "user:name", Account: &userv1.InboundUser_Masque{Masque: &userv1.PasswordAccount{Password: "test-only"}}},
		{Email: "user", Account: &userv1.InboundUser_Wireguard{Wireguard: &userv1.WireGuardAccount{PublicKey: bytes.Repeat([]byte{7}, 32), AllowedIps: []string{"bad"}}}},
	} {
		if _, err := h.AddInboundUser(testutil.VerifiedGRPCContext(t), &userv1.AddInboundUserRequest{InstanceId: "edge", InboundTag: "in", User: user}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("malformed account reached application: %v", err)
		}
	}
	if _, err := h.AddInboundUser(testutil.VerifiedGRPCContext(t), nil); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
}

func TestUserAdditionSanitizesErrorsAndRetainsEffectCertainty(t *testing.T) {
	for _, tc := range []struct {
		cause   error
		code    codes.Code
		unknown bool
	}{
		{instances.ErrNotFound, codes.NotFound, false}, {domain.ErrInboundNotFound, codes.NotFound, false},
		{domain.ErrAPIDisabled, codes.FailedPrecondition, false}, {domain.ErrInboundProtocolMismatch, codes.FailedPrecondition, false},
		{domain.ErrInvalidUserAccount, codes.InvalidArgument, false}, {domain.ErrAPIBusy, codes.ResourceExhausted, false},
		{domain.ErrInvalidInboundResponse, codes.Unavailable, false}, {context.Canceled, codes.Canceled, false},
		{domain.ErrUserMutationUnknown, codes.Unavailable, true}, {errors.Join(domain.ErrUserMutationUnknown, context.DeadlineExceeded), codes.DeadlineExceeded, true},
	} {
		h := grpcapi.Handler{UserAPI: usergrpc.UserAPI{MutationAPI: mutations.MutationAPI{AllowUserManagement: true, UserAddition: userAdder(func(context.Context, instances.InstanceID, domain.InboundUserAddition) (domain.InstanceInboundUserMutation, error) {
			return domain.InstanceInboundUserMutation{}, errors.Join(tc.cause, errors.New("private credential/endpoint"))
		})}}}
		response, err := h.AddInboundUser(testutil.VerifiedGRPCContext(t), &userv1.AddInboundUserRequest{InstanceId: "edge", InboundTag: "in", User: &userv1.InboundUser{Email: "user", Account: &userv1.InboundUser_Vless{Vless: &userv1.VLESSAccount{Id: "native-id"}}}})
		if response != nil || status.Code(err) != tc.code || strings.Contains(err.Error(), "private") {
			t.Fatalf("addition error disclosed/relabelled causes: %v", err)
		}
		details := status.Convert(err).Details()
		if len(details) != 1 {
			t.Fatal("mutation outcome detail missing")
		}
		failure, ok := details[0].(*userv1.UserMutationFailure)
		want := userv1.UserMutationOutcome_USER_MUTATION_OUTCOME_NOT_APPLIED
		if tc.unknown {
			want = userv1.UserMutationOutcome_USER_MUTATION_OUTCOME_UNKNOWN
		}
		if !ok || failure.Outcome != want {
			t.Fatal("mutation certainty changed")
		}
	}
}
