package configuration

import (
	"context"
	"errors"
	"strings"

	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/core"
	"google.golang.org/protobuf/proto"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/accounts"
)

// Limits bound compiled input/output bytes and the total requested users.
// They are admission/output ceilings, not a bound on the Go protobuf heap.
type Limits struct {
	MaxBytes int
	MaxUsers uint32
}

// Preparer projects complete user collections onto a compiled native config.
// It owns no runtime, filesystem state, channels or background work.
type Preparer struct{ limits Limits }

func NewPreparer(limits Limits) (*Preparer, error) {
	if limits.MaxBytes <= 0 || limits.MaxUsers == 0 {
		return nil, errors.New("positive Xray user configuration limits are required")
	}
	return &Preparer{limits: limits}, nil
}

// Prepare borrows a compiled config and boundary-validated accounts synchronously.
// It returns an independent candidate only after every selection succeeds. This
// is not validation of runtime readiness, an engine effect or durable acceptance.
// Work is O(config bytes + inbounds + selections + account bytes), with no native
// calls or per-user goroutines. Native protocol identity/duplicate rules still apply.
func (p *Preparer) Prepare(ctx context.Context, compiled []byte, sets []domain.InboundUserSet) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(compiled) > p.limits.MaxBytes {
		return nil, domain.ErrUserConfigurationTooLarge
	}
	if len(compiled) == 0 {
		return nil, domain.ErrInvalidUserConfiguration
	}
	var config core.Config
	if err := proto.Unmarshal(compiled, &config); err != nil {
		return nil, errors.Join(domain.ErrInvalidUserConfiguration, err)
	}
	// A duplicate selection fails as it is visited; do not allocate by unchecked
	// requested cardinality. An ambiguous native tag cannot select one inbound.
	inbounds := make(map[string]*core.InboundHandlerConfig, len(config.Inbound))
	for _, inbound := range config.Inbound {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if inbound == nil {
			return nil, domain.ErrInvalidUserConfiguration
		}
		if _, exists := inbounds[inbound.Tag]; exists {
			inbounds[inbound.Tag] = nil
		} else {
			inbounds[inbound.Tag] = inbound
		}
	}
	seen := make(map[string]bool)
	var count uint64
	for _, set := range sets {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if strings.TrimSpace(set.InboundTag) == "" {
			return nil, domain.ErrInvalidInboundTag
		}
		if seen[set.InboundTag] {
			return nil, domain.ErrInvalidUserConfiguration
		}
		seen[set.InboundTag] = true
		inbound, found := inbounds[set.InboundTag]
		if !found {
			return nil, domain.ErrInboundNotFound
		}
		if inbound == nil || inbound.ProxySettings == nil {
			return nil, domain.ErrInvalidUserConfiguration
		}
		count += uint64(len(set.Users))
		if count > uint64(p.limits.MaxUsers) {
			return nil, domain.ErrUserCollectionTooLarge
		}
	}
	encodedBytes := 0
	for _, set := range sets {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		inbound := inbounds[set.InboundTag]
		users := make([]*protocol.User, 0, len(set.Users))
		var accountType string
		for _, item := range set.Users {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			user, kind, err := accounts.EncodeUser(item)
			if err != nil {
				return nil, err
			}
			if accountType != "" && accountType != kind {
				return nil, domain.ErrInboundProtocolMismatch
			}
			accountType = kind
			size := proto.Size(user)
			if size > p.limits.MaxBytes-encodedBytes {
				return nil, domain.ErrUserConfigurationTooLarge
			}
			encodedBytes += size
			users = append(users, user)
		}
		if err := replaceUsers(inbound.ProxySettings, users, accountType); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if proto.Size(&config) > p.limits.MaxBytes {
		return nil, domain.ErrUserConfigurationTooLarge
	}
	result, err := proto.Marshal(&config)
	if err != nil {
		return nil, errors.Join(domain.ErrInvalidUserConfiguration, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
