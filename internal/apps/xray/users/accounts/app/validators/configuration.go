package validators

import (
	"context"

	configs "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
)

func UserSets(ctx context.Context, sets []domain.InboundUserSet) error {
	if len(sets) == 0 {
		return domain.ErrInvalidArgument
	}
	if len(sets) > domain.MaxConfigurationUsers {
		return domain.ErrCapacity
	}
	seen := make(map[string]bool, len(sets))
	remaining, count := configs.MaxCompiledBytes, 0
	for _, set := range sets {
		if err := ctx.Err(); err != nil {
			return err
		}
		if Selector(set.InboundTag) != nil {
			return domain.ErrInvalidArgument
		}
		if seen[set.InboundTag] {
			return domain.ErrInvalidArgument
		}
		count += len(set.Accounts)
		if count > domain.MaxConfigurationUsers {
			return domain.ErrCapacity
		}
		for _, raw := range set.Accounts {
			if err := Configuration(raw); err != nil {
				return err
			}
			if len(raw) > remaining {
				return domain.ErrCapacity
			}
			remaining -= len(raw)
		}
		seen[set.InboundTag] = true
	}
	return nil
}
