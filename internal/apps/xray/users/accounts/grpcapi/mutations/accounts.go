package mutations

import (
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	userv1 "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/grpcapi/proto/v1"
)

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
