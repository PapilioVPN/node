package grpcapi_test

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

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/grpcapi"
	xrayv1 "github.com/PapilioVPN/node/internal/modules/engines/xray/grpcapi/proto/v1"
)

type userAdder func(context.Context, instances.InstanceID, domain.InboundUserAddition) (domain.InstanceInboundUserMutation, error)

func (f userAdder) AddInboundUser(ctx context.Context, id instances.InstanceID, addition domain.InboundUserAddition) (domain.InstanceInboundUserMutation, error) {
	return f(ctx, id, addition)
}

func TestUserAdditionChecksIdentityAndIndependentGrantBeforeShape(t *testing.T) {
	if _, err := (grpcapi.Handler{AllowUserManagement: true}).AddInboundUser(t.Context(), nil); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("unverified caller reached addition: %v", err)
	}
	if _, err := (grpcapi.Handler{AllowInfoRead: true, AllowStatsRead: true, AllowStatsReset: true, AllowOnlineRead: true}).AddInboundUser(verifiedContext(t), nil); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("other grants enabled addition: %v", err)
	}
}

func TestUserAdditionDecodesEveryTypedAccountAndRetainsLateAcknowledgement(t *testing.T) {
	key, psk := bytes.Repeat([]byte{7}, 32), bytes.Repeat([]byte{8}, 32)
	zero := uint32(0)
	for _, tc := range []struct {
		wire *xrayv1.InboundUser
		want domain.UserAccount
	}{
		{&xrayv1.InboundUser{Account: &xrayv1.InboundUser_Vless{Vless: &xrayv1.VLESSAccount{Id: "native-id", Flow: "xtls-rprx-vision"}}}, domain.UserAccount{Protocol: domain.VLESS, VLESS: &domain.VLESSAccount{ID: "native-id", Flow: "xtls-rprx-vision"}}},
		{&xrayv1.InboundUser{Account: &xrayv1.InboundUser_Vmess{Vmess: &xrayv1.VMessAccount{Id: "native-id"}}}, domain.UserAccount{Protocol: domain.VMess, VMess: &domain.VMessAccount{ID: "native-id"}}},
		{&xrayv1.InboundUser{Account: &xrayv1.InboundUser_Trojan{Trojan: &xrayv1.PasswordAccount{Password: " password "}}}, domain.UserAccount{Protocol: domain.Trojan, Trojan: &domain.PasswordAccount{Password: " password "}}},
		{&xrayv1.InboundUser{Account: &xrayv1.InboundUser_Shadowsocks{Shadowsocks: &xrayv1.ShadowsocksAccount{Password: " password ", Cipher: xrayv1.ShadowsocksCipher_SHADOWSOCKS_CIPHER_XCHACHA20_POLY1305, IvCheck: true}}}, domain.UserAccount{Protocol: domain.Shadowsocks, Shadowsocks: &domain.ShadowsocksAccount{Password: " password ", Cipher: domain.XChaCha20Poly1305, IVCheck: true}}},
		{&xrayv1.InboundUser{Account: &xrayv1.InboundUser_Shadowsocks_2022{Shadowsocks_2022: &xrayv1.Shadowsocks2022Account{Key: key}}}, domain.UserAccount{Protocol: domain.Shadowsocks2022, Shadowsocks2022: &domain.Shadowsocks2022Account{Key: key}}},
		{&xrayv1.InboundUser{Account: &xrayv1.InboundUser_Hysteria{Hysteria: &xrayv1.HysteriaAccount{Auth: " auth "}}}, domain.UserAccount{Protocol: domain.Hysteria, Hysteria: &domain.HysteriaAccount{Auth: " auth "}}},
		{&xrayv1.InboundUser{Account: &xrayv1.InboundUser_Masque{Masque: &xrayv1.PasswordAccount{Password: " password "}}}, domain.UserAccount{Protocol: domain.MASQUE, MASQUE: &domain.PasswordAccount{Password: " password "}}},
		{&xrayv1.InboundUser{Account: &xrayv1.InboundUser_Wireguard{Wireguard: &xrayv1.WireGuardAccount{PublicKey: key, PreSharedKey: psk, AllowedIps: []string{"10.0.0.2/32", "fd00::2/128"}, KeepAliveSeconds: &zero}}}, domain.UserAccount{Protocol: domain.WireGuard, WireGuard: &domain.WireGuardAccount{PublicKey: key, PreSharedKey: psk, AllowedIPs: []string{"10.0.0.2/32", "fd00::2/128"}, KeepAliveSeconds: &zero}}},
		{&xrayv1.InboundUser{Account: &xrayv1.InboundUser_Wireguard{Wireguard: &xrayv1.WireGuardAccount{PublicKey: key, AllowedIps: []string{"10.0.0.2/32"}}}}, domain.UserAccount{Protocol: domain.WireGuard, WireGuard: &domain.WireGuardAccount{PublicKey: key, AllowedIPs: []string{"10.0.0.2/32"}}}},
	} {
		t.Run(string(tc.want.Protocol), func(t *testing.T) {
			ctx, cancel := context.WithCancel(verifiedContext(t))
			defer cancel()
			tc.wire.Email, tc.wire.Level = " Mixed@Example ", math.MaxUint32
			ack := time.UnixMilli(1700000000123)
			calls := 0
			h := grpcapi.Handler{AllowUserManagement: true, UserAddition: userAdder(func(received context.Context, id instances.InstanceID, addition domain.InboundUserAddition) (domain.InstanceInboundUserMutation, error) {
				calls++
				if received != ctx || id != " edge " || addition.InboundTag != " exact>>>tag " || addition.User.Email != tc.wire.Email || addition.User.Level != math.MaxUint32 || !reflect.DeepEqual(addition.User.Account, tc.want) {
					t.Fatal("typed identity/account/context changed")
				}
				cancel()
				return domain.InstanceInboundUserMutation{InstanceID: id, Selection: domain.InboundUserSelection{InboundTag: addition.InboundTag, Email: addition.User.Email}, Acknowledgement: domain.UserMutationAcknowledgement{AcknowledgedAt: ack}}, nil
			})}
			response, err := h.AddInboundUser(ctx, &xrayv1.AddInboundUserRequest{InstanceId: " edge ", InboundTag: " exact>>>tag ", User: tc.wire})
			if err != nil || calls != 1 || response.InstanceId != " edge " || response.InboundTag != " exact>>>tag " || response.Email != tc.wire.Email || response.AcknowledgedAtUnixMs != ack.UnixMilli() {
				t.Fatalf("confirmed addition was lost/replayed: %v", err)
			}
		})
	}
}

func TestUserAdditionRejectsMissingOrMalformedAccountsBeforeApplication(t *testing.T) {
	h := grpcapi.Handler{AllowUserManagement: true} // A call into the application would panic.
	for _, user := range []*xrayv1.InboundUser{
		nil, {}, {Email: "user"}, {Email: "user", Account: &xrayv1.InboundUser_Vless{}},
		{Email: "user", Account: &xrayv1.InboundUser_Trojan{Trojan: &xrayv1.PasswordAccount{}}},
		{Email: "user", Account: &xrayv1.InboundUser_Shadowsocks{Shadowsocks: &xrayv1.ShadowsocksAccount{Password: "test-only", Cipher: 100}}},
		{Email: "user", Account: &xrayv1.InboundUser_Shadowsocks_2022{Shadowsocks_2022: &xrayv1.Shadowsocks2022Account{Key: []byte{7}}}},
		{Email: "user:name", Account: &xrayv1.InboundUser_Masque{Masque: &xrayv1.PasswordAccount{Password: "test-only"}}},
		{Email: "user", Account: &xrayv1.InboundUser_Wireguard{Wireguard: &xrayv1.WireGuardAccount{PublicKey: bytes.Repeat([]byte{7}, 32), AllowedIps: []string{"bad"}}}},
	} {
		if _, err := h.AddInboundUser(verifiedContext(t), &xrayv1.AddInboundUserRequest{InstanceId: "edge", InboundTag: "in", User: user}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("malformed account reached application: %v", err)
		}
	}
	if _, err := h.AddInboundUser(verifiedContext(t), nil); status.Code(err) != codes.InvalidArgument {
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
		h := grpcapi.Handler{AllowUserManagement: true, UserAddition: userAdder(func(context.Context, instances.InstanceID, domain.InboundUserAddition) (domain.InstanceInboundUserMutation, error) {
			return domain.InstanceInboundUserMutation{}, errors.Join(tc.cause, errors.New("private credential/endpoint"))
		})}
		response, err := h.AddInboundUser(verifiedContext(t), &xrayv1.AddInboundUserRequest{InstanceId: "edge", InboundTag: "in", User: &xrayv1.InboundUser{Email: "user", Account: &xrayv1.InboundUser_Vless{Vless: &xrayv1.VLESSAccount{Id: "native-id"}}}})
		if response != nil || status.Code(err) != tc.code || strings.Contains(err.Error(), "private") {
			t.Fatalf("addition error disclosed/relabelled causes: %v", err)
		}
		details := status.Convert(err).Details()
		if len(details) != 1 {
			t.Fatal("mutation outcome detail missing")
		}
		failure, ok := details[0].(*xrayv1.UserMutationFailure)
		want := xrayv1.UserMutationOutcome_USER_MUTATION_OUTCOME_NOT_APPLIED
		if tc.unknown {
			want = xrayv1.UserMutationOutcome_USER_MUTATION_OUTCOME_UNKNOWN
		}
		if !ok || failure.Outcome != want {
			t.Fatal("mutation certainty changed")
		}
	}
}
