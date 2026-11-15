package validators

import (
	"net/netip"
	"strings"
	"unicode/utf8"

	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
)

func InboundUserSelection(selection domain.InboundUserSelection) error {
	if strings.TrimSpace(selection.InboundTag) == "" || strings.TrimSpace(selection.Email) == "" || !utf8.ValidString(selection.InboundTag) || !utf8.ValidString(selection.Email) {
		return domain.ErrInvalidUserSelection
	}
	return nil
}

func InboundUserAddition(addition domain.InboundUserAddition) error {
	if err := InboundUserSelection(domain.InboundUserSelection{InboundTag: addition.InboundTag, Email: addition.User.Email}); err != nil {
		return err
	}
	a := addition.User.Account
	count := 0
	for _, present := range [8]bool{a.VLESS != nil, a.VMess != nil, a.Trojan != nil, a.Shadowsocks != nil, a.Shadowsocks2022 != nil, a.Hysteria != nil, a.MASQUE != nil, a.WireGuard != nil} {
		if present {
			count++
		}
	}
	if count != 1 {
		return domain.ErrInvalidUserAccount
	}
	validText := func(value string) bool { return value != "" && utf8.ValidString(value) }
	switch a.Protocol {
	case domain.VLESS:
		if a.VLESS != nil && validText(a.VLESS.ID) && utf8.ValidString(a.VLESS.Flow) {
			return nil
		}
	case domain.VMess:
		if a.VMess != nil && validText(a.VMess.ID) {
			return nil
		}
	case domain.Trojan:
		if a.Trojan != nil && validText(a.Trojan.Password) {
			return nil
		}
	case domain.MASQUE:
		if a.MASQUE != nil && validText(a.MASQUE.Password) && !strings.Contains(addition.User.Email, ":") {
			return nil
		}
	case domain.Hysteria:
		if a.Hysteria != nil && validText(a.Hysteria.Auth) {
			return nil
		}
	case domain.Shadowsocks:
		if a.Shadowsocks != nil && validText(a.Shadowsocks.Password) {
			switch a.Shadowsocks.Cipher {
			case domain.AES128GCM, domain.AES256GCM, domain.ChaCha20Poly1305, domain.XChaCha20Poly1305, domain.CipherNone:
				return nil
			}
		}
	case domain.Shadowsocks2022:
		if a.Shadowsocks2022 != nil && (len(a.Shadowsocks2022.Key) == 16 || len(a.Shadowsocks2022.Key) == 32) {
			return nil
		}
	case domain.WireGuard:
		if a.WireGuard == nil || len(a.WireGuard.PublicKey) != 32 || len(a.WireGuard.PreSharedKey) != 0 && len(a.WireGuard.PreSharedKey) != 32 || len(a.WireGuard.AllowedIPs) == 0 || a.WireGuard.KeepAliveSeconds != nil && *a.WireGuard.KeepAliveSeconds > 65535 {
			return domain.ErrInvalidUserAccount
		}
		for _, prefix := range a.WireGuard.AllowedIPs {
			if _, err := netip.ParsePrefix(prefix); err != nil {
				return domain.ErrInvalidUserAccount
			}
		}
		return nil
	}
	return domain.ErrInvalidUserAccount
}
