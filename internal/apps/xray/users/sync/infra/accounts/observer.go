package accounts

import (
	"context"

	upstream "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/domain"
)

type Source interface {
	Status(context.Context, string) (upstream.ControlStatus, error)
	ReadMembership(context.Context, string, string) (upstream.Membership, error)
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

// Snapshot reads the inbound catalog once and reads membership once per selected
// inbound. Empty selection reads local execution and config revisions. Direct
// native and other capability writers can still change the observed state.
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
			membership, err := o.Source.ReadMembership(ctx, id, inbound.Tag)
			if err != nil {
				return domain.Snapshot{}, err
			}
			if membership.Generation != before.Generation || membership.InstanceID != id || membership.InboundTag != inbound.Tag {
				return domain.Snapshot{}, domain.ErrStale
			}
			count += len(membership.Users)
			if count > domain.MaxUsers {
				return domain.Snapshot{}, domain.ErrCapacity
			}
			in := domain.Inbound{
				Tag: inbound.Tag, AccountType: inbound.AccountType, EmailMatching: inbound.EmailMatching,
				Live: inbound.LiveAccounts, Users: make([]domain.Member, 0, len(membership.Users)),
			}
			for _, user := range membership.Users {
				if err := ctx.Err(); err != nil {
					return domain.Snapshot{}, err
				}
				size += len(user.Configuration) + len(user.Email) + 128
				if size > domain.MaxBytes {
					return domain.Snapshot{}, domain.ErrCapacity
				}
				member, err := (Codec{}).Observe(user)
				if err != nil {
					return domain.Snapshot{}, err
				}
				if member.AccountType != inbound.AccountType || member.Email != user.Email {
					return domain.Snapshot{}, domain.ErrInvalid
				}
				in.Users = append(in.Users, member)
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
