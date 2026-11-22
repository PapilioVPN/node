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

// PrepareMany compiles once, indexes requested inbounds in one pass and scans
// each selected native user collection once. It never reads live membership.
// Work is O(compiled bytes + selections + selected output bytes), bounded by
// the existing compiled, per-account and complete live-batch byte limits.
func (p Preparer) PrepareMany(ctx context.Context, id string, selections []domain.SourceSelection, sources []configs.Source) ([]domain.SourceAccount, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(selections) == 0 {
		return nil, domain.ErrInvalidArgument
	}
	if len(selections) > domain.MaxBatchItems {
		return nil, domain.ErrCapacity
	}
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
	type inboundSelection struct {
		inbound   *core.InboundHandlerConfig
		ambiguous bool
		users     map[string]domain.SourceAccount
	}
	selected := make(map[string]*inboundSelection)
	for _, selection := range selections {
		if selected[selection.InboundTag] == nil {
			selected[selection.InboundTag] = &inboundSelection{users: make(map[string]domain.SourceAccount)}
		}
		selected[selection.InboundTag].users[selection.Email] = domain.SourceAccount{Err: domain.ErrInvalidArgument}
	}
	for _, inbound := range configuration.Inbound {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if inbound == nil || selected[inbound.Tag] == nil {
			continue
		}
		selection := selected[inbound.Tag]
		selection.ambiguous = selection.inbound != nil
		selection.inbound = inbound
	}
	for _, selection := range selected {
		inbound := selection.inbound
		var err error
		if inbound == nil || selection.ambiguous || inbound.ProxySettings == nil {
			err = domain.ErrInvalidArgument
		} else {
			err = selectUsers(ctx, inbound.ProxySettings.Type, inbound.ProxySettings.Value, selection.users)
		}
		if err != nil {
			for email := range selection.users {
				selection.users[email] = domain.SourceAccount{Err: err}
			}
		}
	}
	result := make([]domain.SourceAccount, len(selections))
	remaining := domain.MaxBatchBytes
	for i, selection := range selections {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		account := selected[selection.InboundTag].users[selection.Email]
		if len(account.Configuration) > remaining {
			return nil, domain.ErrCapacity
		}
		remaining -= len(account.Configuration)
		result[i] = domain.SourceAccount{Configuration: bytes.Clone(account.Configuration), Err: account.Err}
	}
	return result, nil
}

// selectUsers borrows raw selected User bytes while scanning one proxy payload.
// Unknown protocol fields stay intact; this is structural selection, not native
// semantic credential validation or attestation of the live target's protocol.
func selectUsers(ctx context.Context, name string, content []byte, requested map[string]domain.SourceAccount) error {
	field, account, clientField := userLayout(name)
	if field == 0 {
		return domain.ErrUnsupported
	}
	seen := make(map[string]bool, len(requested))
	isClient := false
	for len(content) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		number, kind, n := protowire.ConsumeTag(content)
		if n < 0 {
			return domain.ErrInvalidArgument
		}
		content = content[n:]
		size := protowire.ConsumeFieldValue(number, kind, content)
		if size < 0 {
			return domain.ErrInvalidArgument
		}
		if number == clientField {
			if kind != protowire.VarintType {
				return domain.ErrInvalidArgument
			}
			value, _ := protowire.ConsumeVarint(content[:size])
			isClient = value != 0
		}
		if number == field {
			if kind != protowire.BytesType {
				return domain.ErrInvalidArgument
			}
			raw, n := protowire.ConsumeBytes(content[:size])
			if n < 0 {
				return domain.ErrInvalidArgument
			}
			var user protocol.User
			if err := proto.Unmarshal(raw, &user); err != nil {
				return errors.Join(domain.ErrInvalidArgument, err)
			}
			if _, ok := requested[user.Email]; ok {
				selected := domain.SourceAccount{Configuration: raw}
				switch {
				case seen[user.Email]:
					selected = domain.SourceAccount{Err: domain.ErrInvalidArgument}
				case len(raw) > domain.MaxAccountBytes:
					selected = domain.SourceAccount{Err: domain.ErrCapacity}
				case user.Account == nil || user.Account.Type != account:
					selected = domain.SourceAccount{Err: domain.ErrInvalidArgument}
				}
				requested[user.Email] = selected
				seen[user.Email] = true
			}
		}
		content = content[size:]
	}
	if isClient {
		return domain.ErrUnsupported
	}
	return ctx.Err()
}
