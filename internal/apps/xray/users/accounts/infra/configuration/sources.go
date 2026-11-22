// Package configuration selects native accounts from bounded compiled sources.
package configuration

import (
	"bytes"
	"context"
	"errors"

	configs "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/core"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

type Compiler interface {
	Compile(context.Context, string, []configs.Source) (configs.Compilation, error)
}
type Preparer struct{ Compiler Compiler }

// Prepare compiles the whole ordered source set once and selects one exact
// inbound/email. It applies no handlers, features, source environment or peers.
func (p Preparer) Prepare(ctx context.Context, id, tag, email string, sources []configs.Source) ([]byte, error) {
	compiled, err := p.Compiler.Compile(ctx, id, sources)
	if err != nil {
		return nil, compileError(err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(compiled.Content) > configs.MaxCompiledBytes {
		return nil, domain.ErrCapacity
	}
	var configuration core.Config
	if err := proto.Unmarshal(compiled.Content, &configuration); err != nil {
		return nil, errors.Join(domain.ErrInvalidArgument, err)
	}
	var selected *core.InboundHandlerConfig
	for _, inbound := range configuration.Inbound {
		if inbound != nil && inbound.Tag == tag {
			if selected != nil {
				return nil, domain.ErrInvalidArgument
			}
			selected = inbound
		}
	}
	if selected == nil || selected.ProxySettings == nil {
		return nil, domain.ErrInvalidArgument
	}
	return selectUser(ctx, selected.ProxySettings.Type, selected.ProxySettings.Value, email)
}

// userLayout follows the pinned native proxy config.proto contracts. These
// packages co-locate generated messages with full server implementations; the
// native wire projection avoids linking those servers into the control process.
// Native compiler integration tests verify every supported layout at the pin.
func userLayout(name string) (field protowire.Number, account string, clientField protowire.Number) {
	switch name {
	case "xray.proxy.vless.inbound.Config":
		return 1, "xray.proxy.vless.Account", 0
	case "xray.proxy.vmess.inbound.Config":
		return 1, "xray.proxy.vmess.Account", 0
	case "xray.proxy.trojan.ServerConfig":
		return 1, "xray.proxy.trojan.Account", 0
	case "xray.proxy.shadowsocks.ServerConfig":
		return 1, "xray.proxy.shadowsocks.Account", 0
	case "xray.proxy.shadowsocks_2022.MultiUserServerConfig":
		return 3, "xray.proxy.shadowsocks_2022.Account", 0
	case "xray.proxy.wireguard.DeviceConfig":
		return 5, "xray.proxy.wireguard.PeerConfig", 8
	case "xray.proxy.hysteria.ServerConfig":
		return 1, "xray.proxy.hysteria.account.Account", 0
	case "xray.proxy.masque.ServerConfig":
		return 1, "xray.proxy.masque.Account", 0
	default:
		return 0, "", 0
	}
}
func selectUser(ctx context.Context, name string, content []byte, email string) ([]byte, error) {
	field, account, clientField := userLayout(name)
	if field == 0 {
		return nil, domain.ErrUnsupported
	}
	var selected []byte
	var isClient bool
	for len(content) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		number, kind, n := protowire.ConsumeTag(content)
		if n < 0 {
			return nil, domain.ErrInvalidArgument
		}
		content = content[n:]
		size := protowire.ConsumeFieldValue(number, kind, content)
		if size < 0 {
			return nil, domain.ErrInvalidArgument
		}
		if number == clientField && kind == protowire.VarintType {
			value, _ := protowire.ConsumeVarint(content[:size])
			isClient = value != 0
		}
		if number == field {
			if kind != protowire.BytesType {
				return nil, domain.ErrInvalidArgument
			}
			raw, n := protowire.ConsumeBytes(content[:size])
			if n < 0 {
				return nil, domain.ErrInvalidArgument
			}
			var user protocol.User
			if err := proto.Unmarshal(raw, &user); err != nil {
				return nil, errors.Join(domain.ErrInvalidArgument, err)
			}
			if user.Email == email {
				if selected != nil {
					return nil, domain.ErrInvalidArgument
				}
				if len(raw) > domain.MaxAccountBytes {
					return nil, domain.ErrCapacity
				}
				if user.Account == nil || user.Account.Type != account {
					return nil, domain.ErrInvalidArgument
				}
				selected = raw
			}
		}
		content = content[size:]
	}
	if isClient {
		return nil, domain.ErrUnsupported
	}
	if selected == nil {
		return nil, domain.ErrInvalidArgument
	}
	// Return owned native bytes, preserving unknown fields without a lossy
	// reconstruction of the protocol-specific account or source settings.
	return bytes.Clone(selected), ctx.Err()
}
func compileError(err error) error {
	switch {
	case errors.Is(err, configs.ErrCapacity), errors.Is(err, configs.ErrTooLarge):
		return errors.Join(domain.ErrCapacity, err)
	case errors.Is(err, configs.ErrNotFound):
		return errors.Join(domain.ErrNotFound, err)
	case errors.Is(err, configs.ErrUnsupported), errors.Is(err, configs.ErrClosed):
		return errors.Join(domain.ErrUnsupported, err)
	case errors.Is(err, configs.ErrInvalidArgument), errors.Is(err, configs.ErrRejected):
		return errors.Join(domain.ErrInvalidArgument, err)
	default:
		return err
	}
}
