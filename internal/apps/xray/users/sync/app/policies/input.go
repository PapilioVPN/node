// Package policies owns pure scoped sync validation and change planning.
package policies

import (
	"context"
	"strings"
	"unicode/utf8"

	accounts "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/domain"
)

func Identifier(s string) bool {
	return s != "" && len(s) <= 4096 && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}

// Validate checks the complete envelope before any native observation or effect.
func Validate(ctx context.Context, r domain.Request) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !Identifier(r.InstanceID) || !Identifier(r.OperationID) || len(r.OperationID) > 128 || r.Mode != domain.Delta && r.Mode != domain.Full || len(r.Selection) == 0 {
		return domain.ErrInvalid
	}
	if len(r.Selection) > domain.MaxInbounds {
		return domain.ErrCapacity
	}
	selected := make(map[string]bool, len(r.Selection))
	for _, tag := range r.Selection {
		if !Identifier(tag) || selected[tag] {
			return domain.ErrInvalid
		}
		selected[tag] = true
	}
	seen := make(map[string]bool, len(r.Sets))
	count, size := len(r.Removals), 0
	for _, set := range r.Sets {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !selected[set.InboundTag] || seen[set.InboundTag] || set.Accounts == nil {
			return domain.ErrInvalid
		}
		seen[set.InboundTag] = true
		count += len(set.Accounts)
		if count > domain.MaxUsers {
			return domain.ErrCapacity
		}
		for _, raw := range set.Accounts {
			if len(raw) == 0 {
				return domain.ErrInvalid
			}
			if len(raw) > accounts.MaxAccountBytes {
				return domain.ErrCapacity
			}
			size += len(raw)
			if size > domain.MaxBytes {
				return domain.ErrCapacity
			}
		}
	}
	for _, removal := range r.Removals {
		if !selected[removal.InboundTag] || !Identifier(removal.Email) {
			return domain.ErrInvalid
		}
	}
	if r.Mode == domain.Full {
		if len(r.Removals) != 0 || len(seen) != len(selected) {
			return domain.ErrInvalid
		}
	} else if count > accounts.MaxBatchItems || size > accounts.MaxBatchBytes {
		return domain.ErrCapacity
	}
	return ctx.Err()
}
