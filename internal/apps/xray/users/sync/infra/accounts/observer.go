package accounts

import (
	"context"

	upstream "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/domain"
)

type Source interface {
	Status(context.Context, string) (upstream.ControlStatus, error)
	Export(context.Context, string, string, upstream.PageRequest) (upstream.AccountPage, error)
}
type Catalog interface {
	Select(context.Context, string, []string) ([]upstream.Inbound, error)
}
type ConfigRevision interface {
	Revision(context.Context, string) (uint64, error)
}
type Observer struct {
	Source  Source
	Catalog Catalog
	Configs ConfigRevision
}

// Snapshot uses one Inbounds list for the complete selection and one Accounts
// export per selected inbound, then zero native calls for continuations. Empty
// selection reads only local execution/config revisions. This is read/check
// evidence, not exclusion against direct native or other capability writers.
func (o Observer) Snapshot(ctx context.Context, id string, tags []string) (domain.Snapshot, error) {
	before, err := o.Source.Status(ctx, id)
	if err != nil {
		return domain.Snapshot{}, err
	}
	if before.ExecutionState != "running" {
		return domain.Snapshot{}, upstream.ErrNotRunning
	}
	if before.RequiresInstanceRestart {
		return domain.Snapshot{}, upstream.ErrUncertain
	}
	config, err := o.Configs.Revision(ctx, id)
	if err != nil {
		return domain.Snapshot{}, err
	}
	snapshot := domain.Snapshot{Revision: domain.Revision{Config: config, Generation: before.Generation}}
	if len(tags) != 0 {
		inbounds, err := o.Catalog.Select(ctx, id, tags)
		if err != nil {
			return domain.Snapshot{}, err
		}
		if len(inbounds) != len(tags) {
			return domain.Snapshot{}, domain.ErrInvalid
		}
		count, size := 0, 0
		for i, inbound := range inbounds {
			if inbound.Err != nil {
				return domain.Snapshot{}, inbound.Err
			}
			if inbound.Tag != tags[i] || inbound.Generation != before.Generation {
				return domain.Snapshot{}, domain.ErrStale
			}
			in := domain.Inbound{Tag: inbound.Tag, AccountType: inbound.AccountType, EmailMatching: inbound.EmailMatching, Live: inbound.LiveAccounts}
			cursor := ""
			for {
				page, err := o.Source.Export(ctx, id, inbound.Tag, upstream.PageRequest{Cursor: cursor})
				if err != nil {
					return domain.Snapshot{}, err
				}
				if page.Generation != before.Generation || page.InstanceID != id || page.InboundTag != inbound.Tag || len(page.Users) > upstream.MaxPageSize {
					return domain.Snapshot{}, domain.ErrStale
				}
				for _, user := range page.Users {
					count++
					size += len(user.Configuration) + len(user.Email) + 128
					if count > domain.MaxUsers || size > domain.MaxBytes {
						return domain.Snapshot{}, domain.ErrCapacity
					}
					member, err := (Codec{}).Decode(user.Configuration)
					if err != nil {
						return domain.Snapshot{}, err
					}
					if member.AccountType != inbound.AccountType || member.Email != user.Email {
						return domain.Snapshot{}, domain.ErrInvalid
					}
					in.Users = append(in.Users, member)
				}
				if page.NextCursor == "" {
					break
				}
				if page.NextCursor == cursor || len(page.Users) == 0 {
					return domain.Snapshot{}, domain.ErrInvalid
				}
				cursor = page.NextCursor
			}
			snapshot.Inbounds = append(snapshot.Inbounds, in)
		}
	}
	after, err := o.Source.Status(ctx, id)
	if err != nil {
		return domain.Snapshot{}, err
	}
	finalConfig, err := o.Configs.Revision(ctx, id)
	if err != nil {
		return domain.Snapshot{}, err
	}
	if finalConfig != config || after.Generation != before.Generation || after.ExecutionState != "running" || after.RequiresInstanceRestart {
		return domain.Snapshot{}, domain.ErrStale
	}
	return snapshot, ctx.Err()
}
