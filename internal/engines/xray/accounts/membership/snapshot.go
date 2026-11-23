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

type AccountInput struct {
	Email string  `json:"email"`
	UUID  *string `json:"uuid,omitempty"`
}

type SyncUsersRequest struct {
	Scope    Scope
	Accounts []AccountInput
}

func (s *Service) SyncUsers(ctx context.Context, req SyncUsersRequest) (SyncResult, error) {
	if req.Accounts == nil || len(req.Accounts) > MaxSyncAccounts {
		return SyncResult{}, ErrInvalid
	}
	desired := make(map[string]IDKey, len(req.Accounts))
	ids := make(map[IDKey]struct{}, len(req.Accounts))
	accounts := make([]Account, 0, len(req.Accounts))
	for _, input := range req.Accounts {
		if !validName(input.Email, 1024) || input.UUID == nil {
			return SyncResult{}, ErrInvalid
		}
		id, valid := ParseUUID(*input.UUID)
		if !valid {
			return SyncResult{}, ErrInvalid
		}
		name, key := NameKey(input.Email), id.Key()
		if _, exists := desired[name]; exists {
			return SyncResult{}, ErrInvalid
		}
		if _, exists := ids[key]; exists {
			return SyncResult{}, ErrInvalid
		}
		desired[name] = key
		ids[key] = struct{}{}
		accounts = append(accounts, Account{Email: input.Email, ID: id})
	}

	return s.sync(ctx, req.Scope, batchTimeout, func(editor Editor, result *SyncResult) error {
		var removals []string
		released := make(map[IDKey]struct{})
		for name, id := range editor.Named() {
			if key, keep := desired[name]; !keep || id != key {
				removals = append(removals, name)
				released[id] = struct{}{}
			}
		}
		additions := make([]Account, 0, len(accounts))
		for _, account := range accounts {
			key := desired[NameKey(account.Email)]
			if current, exists := editor.Lookup(account.Email); exists && current == key {
				continue
			}
			if _, free := released[key]; !free && editor.HasID(key) {
				return ErrExists
			}
			additions = append(additions, account)
		}
		if len(additions)-len(removals) > editor.Capacity() {
			return ErrTooLarge
		}
		slices.Sort(removals)
		return applyPlan(editor, result, removals, additions)
	})
}

func applyPlan(editor Editor, result *SyncResult, removals []string, additions []Account) error {
	for _, name := range removals {
		if err := result.remove(editor, name); err != nil {
			return err
		}
	}
	for _, account := range additions {
		if err := result.add(editor, account); err != nil {
			return err
		}
	}
	return nil
}
