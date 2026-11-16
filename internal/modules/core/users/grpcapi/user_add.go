package grpcapi

import (
	"context"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/core/users/app/validators"
	"github.com/PapilioVPN/node/internal/modules/core/users/domain"
	userv1 "github.com/PapilioVPN/node/internal/modules/core/users/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/shared/grpcidentity"
)

type InboundUserAdder interface {
	AddInboundUser(context.Context, instances.InstanceID, domain.InboundUserAddition) (domain.InstanceInboundUserMutation, error)
}

func (h UserAPI) AddInboundUser(ctx context.Context, request *userv1.AddInboundUserRequest) (*userv1.AddInboundUserResponse, error) {
	if err := grpcidentity.VerifyClient(ctx); err != nil {
		return nil, err
	}
	if !h.AllowUserManagement {
		return nil, status.Error(codes.PermissionDenied, "engine user management is not granted")
	}
	addition := domain.InboundUserAddition{InboundTag: request.GetInboundTag(), User: inboundUser(request.GetUser())}
	if strings.TrimSpace(request.GetInstanceId()) == "" || validators.InboundUserAddition(addition) != nil {
		return nil, status.Error(codes.InvalidArgument, "instance_id, inbound_tag, email and one valid typed account are required")
	}
	result, err := h.UserAddition.AddInboundUser(ctx, instances.InstanceID(request.InstanceId), addition)
	if err != nil {
		return nil, userMutationError(ctx, err)
	}
	return &userv1.AddInboundUserResponse{InstanceId: string(result.InstanceID), InboundTag: result.Selection.InboundTag, Email: result.Selection.Email, AcknowledgedAtUnixMs: result.Acknowledgement.AcknowledgedAt.UnixMilli()}, nil
}

// The generated oneof is decoded only here; domain/app never import protobuf.
func inboundUser(user *userv1.InboundUser) domain.InboundUser {
	result := domain.InboundUser{Email: user.GetEmail(), Level: user.GetLevel()}
	a := &result.Account
	switch account := user.GetAccount().(type) {
	case *userv1.InboundUser_Vless:
		a.Protocol, a.VLESS = domain.VLESS, &domain.VLESSAccount{ID: account.Vless.GetId(), Flow: account.Vless.GetFlow()}
	case *userv1.InboundUser_Vmess:
		a.Protocol, a.VMess = domain.VMess, &domain.VMessAccount{ID: account.Vmess.GetId()}
	case *userv1.InboundUser_Trojan:
		a.Protocol, a.Trojan = domain.Trojan, &domain.PasswordAccount{Password: account.Trojan.GetPassword()}
	case *userv1.InboundUser_Shadowsocks:
		a.Protocol, a.Shadowsocks = domain.Shadowsocks, &domain.ShadowsocksAccount{Password: account.Shadowsocks.GetPassword(), Cipher: shadowsocksCipher(account.Shadowsocks.GetCipher()), IVCheck: account.Shadowsocks.GetIvCheck()}
	case *userv1.InboundUser_Shadowsocks_2022:
		a.Protocol, a.Shadowsocks2022 = domain.Shadowsocks2022, &domain.Shadowsocks2022Account{Key: account.Shadowsocks_2022.GetKey()}
	case *userv1.InboundUser_Hysteria:
		a.Protocol, a.Hysteria = domain.Hysteria, &domain.HysteriaAccount{Auth: account.Hysteria.GetAuth()}
	case *userv1.InboundUser_Masque:
		a.Protocol, a.MASQUE = domain.MASQUE, &domain.PasswordAccount{Password: account.Masque.GetPassword()}
	case *userv1.InboundUser_Wireguard:
		a.Protocol, a.WireGuard = domain.WireGuard, &domain.WireGuardAccount{PublicKey: account.Wireguard.GetPublicKey(), PreSharedKey: account.Wireguard.GetPreSharedKey(), AllowedIPs: account.Wireguard.GetAllowedIps()}
		if account.Wireguard != nil {
			a.WireGuard.KeepAliveSeconds = account.Wireguard.KeepAliveSeconds
		}
	}
	return result
}

func shadowsocksCipher(cipher userv1.ShadowsocksCipher) domain.ShadowsocksCipher {
	switch cipher {
	case userv1.ShadowsocksCipher_SHADOWSOCKS_CIPHER_AES_128_GCM:
		return domain.AES128GCM
	case userv1.ShadowsocksCipher_SHADOWSOCKS_CIPHER_AES_256_GCM:
		return domain.AES256GCM
	case userv1.ShadowsocksCipher_SHADOWSOCKS_CIPHER_CHACHA20_POLY1305:
		return domain.ChaCha20Poly1305
	case userv1.ShadowsocksCipher_SHADOWSOCKS_CIPHER_XCHACHA20_POLY1305:
		return domain.XChaCha20Poly1305
	case userv1.ShadowsocksCipher_SHADOWSOCKS_CIPHER_NONE:
		return domain.CipherNone
	default:
		return ""
	}
}
