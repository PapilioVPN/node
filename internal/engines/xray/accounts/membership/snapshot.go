package membership

import (
	"context"
	"slices"
	"time"
)

const (
	MaxSyncAccounts = 1000
	batchTimeout    = 5 * time.Second
)

type SyncUsersRequest struct {
	Scope       Scope
	InboundTags []string
	Accounts    []AccountInput
}

type change struct {
	email      string
	credential *Credential
}

func (s *Service) SyncUsers(ctx context.Context, req SyncUsersRequest) (SyncResult, error) {
	return s.reconcile(ctx, req.Scope, req.InboundTags, batchTimeout, req.Accounts, true)
}

type plan struct {
	removals  []string
	additions []PreparedAccount
	targets   []PlannedMember
}

func preparePlan(editor Editor, changes []change, full bool) (plan, error) {
	var plan plan
	names := make(map[string]bool, len(changes))
	ids := make(map[IDKey]bool, len(changes))
	released := make(map[IDKey]bool)

	for _, change := range changes {
		name, err := editor.Name(change.email)
		if err != nil || names[name] {
			return plan, ErrInvalid
		}
		names[name] = true
		current, exists := editor.Lookup(name)
		target := PlannedMember{Name: name}
		if change.credential != nil {
			account, err := editor.Prepare(change.email, *change.credential)
			if err != nil {
				return plan, err
			}
			if ids[account.Identity] {
				return plan, ErrInvalid
			}
			ids[account.Identity] = true
			if exists && (current == account.Key || account.KeepVersion && current.Identity == account.Identity) {
				target.Keep = true
				plan.targets = append(plan.targets, target)
				continue
			}
			target.Replacement = &account
			plan.additions = append(plan.additions, account)
		}
		plan.targets = append(plan.targets, target)
		if exists {
			plan.removals = append(plan.removals, name)
			released[current.Identity] = true
		}
	}
	if full {
		for name, current := range editor.Named() {
			if !names[name] {
				plan.removals = append(plan.removals, name)
				released[current.Identity] = true
			}
		}
		slices.Sort(plan.removals)
	}
	for _, account := range plan.additions {
		if !released[account.Identity] && editor.HasID(account.Identity) {
			return plan, ErrExists
		}
	}
	return plan, nil
}

func applyPlans(session EditSession, groups [][]change, full bool, result *SyncResult) error {
	editors := session.Inbounds()
	plans := make([]plan, len(editors))
	var growth int
	for i, editor := range editors {
		plan, err := preparePlan(editor, groups[i], full)
		if err != nil {
			return err
		}
		if err := editor.Stage(full, plan.targets); err != nil {
			return err
		}
		plans[i] = plan
		growth += len(plan.additions) - len(plan.removals)
	}
	if growth > editors[0].Capacity() {
		return ErrTooLarge
	}
	if err := session.CheckStaged(); err != nil {
		return err
	}
	for i, plan := range plans {
		for _, name := range plan.removals {
			if err := result.remove(editors[i], name); err != nil {
				return err
			}
		}
	}
	for i, plan := range plans {
		for _, account := range plan.additions {
			if err := result.add(editors[i], account); err != nil {
				return err
			}
		}
	}
	return nil
}
