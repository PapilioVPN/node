package configuration

import (
	"context"
	"errors"

	configs "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

const MaxPreparedUsers = domain.MaxConfigurationUsers

// ReplaceUsers builds one owned native config without I/O or live effects. It
// scans inbounds once and replaces only selected account fields. Input/output
// are bounded to the existing compiled-config ceiling; these are byte/count
// limits, not a hard protobuf heap bound. Native semantic validation is separate.
func ReplaceUsers(ctx context.Context, content []byte, sets []domain.InboundUserSet) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(content) > configs.MaxCompiledBytes {
		return nil, domain.ErrCapacity
	}
	if err := validators.UserSets(ctx, sets); err != nil {
		return nil, err
	}
	byTag := make(map[string]domain.InboundUserSet, len(sets))
	for _, set := range sets {
		byTag[set.InboundTag] = set
	}
	var candidate core.Config
	if err := proto.Unmarshal(content, &candidate); err != nil {
		return nil, errors.Join(domain.ErrInvalidArgument, err)
	}
	seen := make(map[string]bool, len(sets))
	for _, inbound := range candidate.Inbound {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if inbound == nil {
			continue
		}
		set, selected := byTag[inbound.Tag]
		if !selected {
			continue
		}
		if seen[inbound.Tag] || inbound.ProxySettings == nil {
			return nil, domain.ErrInvalidArgument
		}
		seen[inbound.Tag] = true
		replacement, err := replaceAccounts(ctx, inbound.ProxySettings, set.Accounts)
		if err != nil {
			return nil, err
		}
		inbound.ProxySettings = replacement
	}
	if len(seen) != len(sets) {
		return nil, domain.ErrInvalidArgument
	}
	if proto.Size(&candidate) > configs.MaxCompiledBytes {
		return nil, domain.ErrCapacity
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return proto.Marshal(&candidate)
}

func replaceAccounts(ctx context.Context, settings *serial.TypedMessage, accounts [][]byte) (*serial.TypedMessage, error) {
	name, content := settings.Type, settings.Value
	if name == "xray.proxy.shadowsocks_2022.ServerConfig" {
		var err error
		content, err = multiUserShadowsocks(content)
		if err != nil {
			return nil, err
		}
		name = "xray.proxy.shadowsocks_2022.MultiUserServerConfig"
	}
	field, accountType, clientField := userLayout(name)
	if field == 0 {
		return nil, domain.ErrUnsupported
	}
	var output []byte
	isClient := false
	for len(content) != 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		number, kind, n := protowire.ConsumeTag(content)
		if n < 0 {
			return nil, domain.ErrInvalidArgument
		}
		size := protowire.ConsumeFieldValue(number, kind, content[n:])
		if size < 0 {
			return nil, domain.ErrInvalidArgument
		}
		if number == clientField && kind == protowire.VarintType {
			value, _ := protowire.ConsumeVarint(content[n : n+size])
			isClient = value != 0
		}
		if number != field {
			output = append(output, content[:n+size]...)
		} else if kind != protowire.BytesType {
			return nil, domain.ErrInvalidArgument
		}
		content = content[n+size:]
	}
	if isClient {
		return nil, domain.ErrUnsupported
	}
	for _, raw := range accounts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var account protocol.User
		if err := proto.Unmarshal(raw, &account); err != nil {
			return nil, errors.Join(domain.ErrInvalidArgument, err)
		}
		if account.Account == nil || account.Account.Type != accountType || validators.Email(account.Email) != nil {
			return nil, domain.ErrInvalidArgument
		}
		output = protowire.AppendBytes(protowire.AppendTag(output, field, protowire.BytesType), raw)
	}
	// settings belongs to this private decoded candidate; no extra payload copy.
	settings.Type, settings.Value = name, output
	return settings, nil
}
