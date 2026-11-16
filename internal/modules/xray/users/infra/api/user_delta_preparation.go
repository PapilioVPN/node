package api

import (
	"context"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	native "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/common/serial"

	"github.com/PapilioVPN/node/internal/modules/xray/users/domain"
)

func prepareUserDelta(ctx context.Context, input domain.InboundUserDelta) ([]*serial.TypedMessage, string, error) {
	if input.InboundTag == "" || !utf8.ValidString(input.InboundTag) {
		return nil, "", domain.ErrInvalidInboundTag
	}
	operations := make([]*serial.TypedMessage, len(input.Users))
	var expected string
	for i, item := range input.Users {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		if item.Email == "" {
			return nil, "", domain.ErrInvalidUserSelection
		}
		user, kind, err := nativeUser(item)
		if err != nil {
			return nil, "", err
		}
		if i == 0 {
			expected = kind
		} else if expected != kind {
			return nil, "", domain.ErrInboundProtocolMismatch
		}
		operation, err := encodeUserOperation(&native.AddUserOperation{User: user})
		if err != nil {
			return nil, "", errors.Join(domain.ErrInvalidUserAccount, err)
		}
		operations[i] = operation
	}
	for _, email := range input.RemoveEmails {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		if email == "" || !utf8.ValidString(email) {
			return nil, "", domain.ErrInvalidUserSelection
		}
	}
	return operations, expected, nil
}

// deltaIdentityKey follows pinned native managers. Hysteria/WireGuard use exact
// matching; legacy Shadowsocks uses EqualFold and permits duplicate emails.
// Other implemented managers index lowercase emails. This is not a global policy.
func deltaIdentityKey(kind string) (func(string) string, bool, error) {
	switch kind {
	case "xray.proxy.wireguard.DeviceConfig", "xray.proxy.hysteria.ServerConfig":
		return func(email string) string { return email }, true, nil
	case "xray.proxy.shadowsocks.ServerConfig":
		return foldEmail, true, nil
	case "xray.proxy.vless.inbound.Config", "xray.proxy.vmess.inbound.Config", "xray.proxy.trojan.ServerConfig", "xray.proxy.shadowsocks_2022.MultiUserServerConfig", "xray.proxy.masque.ServerConfig":
		return strings.ToLower, false, nil
	default:
		return nil, false, domain.ErrUserManagementUnsupported
	}
}

// EqualFold has no indexed key API. The minimum rune in each native SimpleFold
// cycle gives its equivalence in linear string space/time, without scanning all
// observed members once per requested user.
func foldEmail(email string) string {
	return strings.Map(func(r rune) rune {
		minimum := r
		for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
			if next < minimum {
				minimum = next
			}
		}
		return minimum
	}, email)
}
