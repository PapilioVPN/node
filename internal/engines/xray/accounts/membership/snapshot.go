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
	Scope    Scope
	Accounts []AccountInput
}

type change struct {
	email      string
	credential *Credential
}

func changesFrom(accounts []AccountInput, full bool) ([]change, error) {
	if accounts == nil || len(accounts) > MaxSyncAccounts {
		return nil, ErrInvalid
	}
	changes := make([]change, 0, len(accounts))
	for _, input := range accounts {
		credential, err := input.selected()
		if err != nil || (full && credential == nil) {
			return nil, ErrInvalid
		}
		changes = append(changes, change{input.Email, credential})
	}
	return changes, nil
}

func (s *Service) SyncUsers(ctx context.Context, req SyncUsersRequest) (SyncResult, error) {
	changes, err := changesFrom(req.Accounts, true)
	if err != nil {
		return SyncResult{}, err
	}
	return s.reconcile(ctx, req.Scope, batchTimeout, changes, true)
}

type plan struct {
	removals  []string
	additions []PreparedAccount
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
		if change.credential != nil {
			account, err := editor.Prepare(change.email, *change.credential)
			if err != nil {
				return plan, err
			}
			if ids[account.Identity] {
				return plan, ErrInvalid
			}
			ids[account.Identity] = true
			if exists && current == account.Key || exists && account.KeepVersion && current.Identity == account.Identity {
				continue
			}
			plan.additions = append(plan.additions, account)
		}
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
	if len(plan.additions)-len(plan.removals) > editor.Capacity() {
		return plan, ErrTooLarge
	}
	for _, account := range plan.additions {
		if !released[account.Identity] && editor.HasID(account.Identity) {
			return plan, ErrExists
		}
	}
	return plan, nil
}

func (p plan) apply(editor Editor, result *SyncResult) error {
	for _, name := range p.removals {
		if err := result.remove(editor, name); err != nil {
			return err
		}
	}
	for _, account := range p.additions {
		if err := result.add(editor, account); err != nil {
			return err
		}
	}
	return nil
}
