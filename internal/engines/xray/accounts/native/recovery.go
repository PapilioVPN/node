package native

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/PapilioVPN/node/internal/engines/xray/accounts/membership"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

// Initialize restores accepted membership and applies initial snapshots on a
// private candidate. Only a successful lifecycle acceptance commits the result.
func (c *Client) Initialize(ctx context.Context, id string, conn *grpc.ClientConn,
	initial []membership.InboundSnapshot, excluded []string) (func(), error) {
	if err := c.mutation.Acquire(ctx, 1); err != nil {
		return nil, err
	}
	defer c.mutation.Release(1)

	previous := c.accepted.Load()
	if len(initial) == 0 && (previous == nil || len(previous.inbounds) == 0) {
		return nil, nil
	}
	if len(initial) > membership.MaxSyncAccounts {
		return nil, membership.ErrTooLarge
	}
	candidate := New(candidateConnection{conn})
	state, err := candidate.inventory(ctx, id)
	if err != nil {
		return nil, err
	}

	skip := make(map[string]bool, len(excluded))
	for _, tag := range excluded {
		skip[tag] = true
	}
	seeds := make(map[string]bool, len(initial))
	var count int
	for _, snapshot := range initial {
		count += len(snapshot.Accounts)
		if count > membership.MaxSyncAccounts {
			return nil, membership.ErrTooLarge
		}
		if seeds[snapshot.InboundTag] {
			return nil, membership.ErrInvalid
		}
		seeds[snapshot.InboundTag] = true
	}

	retained := previous.clone()
	var restores []restorePlan
	for tag, saved := range retained.inbounds {
		inbound, err := state.selectInbound(tag, false)
		if err != nil || skip[tag] || seeds[tag] || inbound.layout != saved.layout || inbound.ss2022KeySize != saved.keySize {
			delete(retained.inbounds, tag)
			continue
		}
		e, err := candidate.openEditor(ctx, state, membership.Scope{InstanceID: id, InboundTag: tag}, false)
		if err != nil {
			return nil, err
		}
		plan, err := prepareRestore(e, saved)
		if err != nil {
			return nil, err
		}
		restores = append(restores, plan)
	}
	slices.SortFunc(restores, func(a, b restorePlan) int {
		return strings.Compare(a.editor.scope.InboundTag, b.editor.scope.InboundTag)
	})
	if err := applyRestores(state, restores); err != nil {
		return nil, err
	}

	candidate.accepted.Store(retained)
	service := membership.New(candidate)
	for _, snapshot := range initial {
		if skip[snapshot.InboundTag] {
			continue
		}
		result, err := service.SyncUsers(ctx, membership.SyncUsersRequest{
			Scope:    membership.Scope{InstanceID: id, InboundTag: snapshot.InboundTag},
			Accounts: snapshot.Accounts,
		})
		if err != nil {
			return nil, err
		}
		if result.Outcome != membership.Confirmed {
			return nil, fmt.Errorf("initial Xray accounts: %s (%s)", result.Outcome, result.Failure)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	accepted := candidate.accepted.Load()
	return func() {
		c.accepted.Store(accepted)
		c.cached.Store(state)
	}, nil
}

type candidateConnection struct{ conn *grpc.ClientConn }

func (c candidateConnection) NativeConnection(string) (*grpc.ClientConn, error) { return c.conn, nil }

type restorePlan struct {
	editor    *editor
	removals  []string
	additions []membership.PreparedAccount
}

func prepareRestore(e *editor, saved acceptedInbound) (restorePlan, error) {
	plan := restorePlan{editor: e}
	released := make(map[membership.IDKey]bool)
	for name, original := range e.entry.records {
		target, requested := saved.accounts[name]
		if !requested && !saved.full || proto.Equal(original, target) {
			continue
		}
		plan.removals = append(plan.removals, name)
		released[e.entry.names[name].Identity] = true
	}
	ids := make(map[membership.IDKey]bool)
	for name, target := range saved.accounts {
		if target == nil || proto.Equal(e.entry.records[name], target) {
			continue
		}
		value := target.GetAccount().GetValue()
		key, err := e.entry.key(name, value)
		if err != nil {
			return plan, err
		}
		if ids[key.Identity] || !released[key.Identity] && e.HasID(key.Identity) {
			return plan, membership.ErrExists
		}
		ids[key.Identity] = true
		plan.additions = append(plan.additions, membership.PreparedAccount{
			Email: target.Email, Name: name, Key: key, Level: target.Level, Value: value,
		})
	}
	slices.Sort(plan.removals)
	slices.SortFunc(plan.additions, func(a, b membership.PreparedAccount) int {
		return strings.Compare(a.Name, b.Name)
	})
	return plan, nil
}

func applyRestores(state *inventory, restores []restorePlan) error {
	var growth int
	for _, plan := range restores {
		growth += len(plan.additions) - len(plan.removals)
	}
	if state.size+growth > maxAccounts {
		return membership.ErrTooLarge
	}
	for _, plan := range restores {
		for _, name := range plan.removals {
			if err := plan.editor.Remove(name); err != nil {
				return err
			}
		}
	}
	for _, plan := range restores {
		for _, account := range plan.additions {
			if err := plan.editor.Add(account); err != nil {
				return err
			}
		}
	}
	return nil
}
