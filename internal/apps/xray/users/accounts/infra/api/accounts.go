package api

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strconv"

	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/common/uuid"
	hysteria "github.com/xtls/xray-core/proxy/hysteria/account"
	"github.com/xtls/xray-core/proxy/masque"
	"github.com/xtls/xray-core/proxy/shadowsocks"
	ss2022 "github.com/xtls/xray-core/proxy/shadowsocks_2022"
	"github.com/xtls/xray-core/proxy/trojan"
	"github.com/xtls/xray-core/proxy/vless"
	"github.com/xtls/xray-core/proxy/vmess"
	"github.com/xtls/xray-core/proxy/wireguard"
	"google.golang.org/protobuf/proto"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
)

// nativeUser consumes a boundary-validated tagged account. Configuration type
// names for VLESS/VMess/Hysteria are pinned wire contracts; their generated Go
// config packages also embed engine implementations, unnecessary for this client.
func nativeUser(user domain.InboundUser) (*protocol.User, string, error) {
	a := user.Account
	var message proto.Message
	var inboundType string
	switch a.Protocol {
	case domain.VLESS:
		id, err := uuid.ParseString(a.VLESS.ID)
		if err != nil {
			return nil, "", errors.Join(domain.ErrInvalidUserAccount, err)
		}
		message = &vless.Account{Id: id.String(), Flow: a.VLESS.Flow}
		inboundType = "xray.proxy.vless.inbound.Config"
	case domain.VMess:
		id, err := uuid.ParseString(a.VMess.ID)
		if err != nil {
			return nil, "", errors.Join(domain.ErrInvalidUserAccount, err)
		}
		message = &vmess.Account{Id: id.String()}
		inboundType = "xray.proxy.vmess.inbound.Config"
	case domain.Trojan:
		message = &trojan.Account{Password: a.Trojan.Password}
		inboundType = serial.GetMessageType(&trojan.ServerConfig{})
	case domain.Shadowsocks:
		var cipher shadowsocks.CipherType
		switch a.Shadowsocks.Cipher {
		case domain.AES128GCM:
			cipher = shadowsocks.CipherType_AES_128_GCM
		case domain.AES256GCM:
			cipher = shadowsocks.CipherType_AES_256_GCM
		case domain.ChaCha20Poly1305:
			cipher = shadowsocks.CipherType_CHACHA20_POLY1305
		case domain.XChaCha20Poly1305:
			cipher = shadowsocks.CipherType_XCHACHA20_POLY1305
		case domain.CipherNone:
			cipher = shadowsocks.CipherType(9) // Legacy engine wire value, not a fallback.
		default:
			return nil, "", domain.ErrInvalidUserAccount
		}
		message = &shadowsocks.Account{Password: a.Shadowsocks.Password, CipherType: cipher, IvCheck: a.Shadowsocks.IVCheck}
		inboundType = serial.GetMessageType(&shadowsocks.ServerConfig{})
	case domain.Shadowsocks2022:
		message = &ss2022.Account{Key: base64.StdEncoding.EncodeToString(a.Shadowsocks2022.Key)}
		inboundType = serial.GetMessageType(&ss2022.MultiUserServerConfig{})
	case domain.Hysteria:
		message = &hysteria.Account{Auth: a.Hysteria.Auth}
		inboundType = "xray.proxy.hysteria.ServerConfig"
	case domain.MASQUE:
		message = &masque.Account{Password: a.MASQUE.Password}
		inboundType = serial.GetMessageType(&masque.ServerConfig{})
	case domain.WireGuard:
		peer := &wireguard.PeerConfig{PublicKey: hex.EncodeToString(a.WireGuard.PublicKey), AllowedIps: a.WireGuard.AllowedIPs}
		if len(a.WireGuard.PreSharedKey) > 0 {
			peer.PreSharedKey = hex.EncodeToString(a.WireGuard.PreSharedKey)
		}
		if a.WireGuard.KeepAliveSeconds != nil {
			peer.KeepAlive = strconv.FormatUint(uint64(*a.WireGuard.KeepAliveSeconds), 10)
		}
		message = peer
		inboundType = serial.GetMessageType(&wireguard.DeviceConfig{})
	default:
		return nil, "", domain.ErrInvalidUserAccount
	}
	account, err := encodeUserOperation(message)
	if err != nil {
		return nil, "", errors.Join(domain.ErrInvalidUserAccount, err)
	}
	return &protocol.User{Email: user.Email, Level: user.Level, Account: account}, inboundType, nil
}
