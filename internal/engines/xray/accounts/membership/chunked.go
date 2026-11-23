package membership

import (
	"context"
	"errors"
	"io"
	"time"
)

const (
	MaxChunkAccounts = 100
	maxChunks        = 100
	StreamTimeout    = 30 * time.Second
)

type UsersChunk struct {
	Scope    Scope
	Index    uint32
	Last     bool
	Accounts []AccountInput
}

type accountDelta struct {
	account Account
	key     IDKey
	present bool
}

// SyncUsersChunked applies explicit deltas after the final marker and clean EOF.
func (s *Service) SyncUsersChunked(ctx context.Context, receive func() (UsersChunk, error)) (SyncResult, error) {
	select {
	case s.streams <- struct{}{}:
		defer func() { <-s.streams }()
	default:
		return SyncResult{Outcome: NotApplied, Failure: syncFailure(ErrBusy)}, nil
	}

	scope, changes, err := collectDeltas(ctx, receive)
	if err != nil {
		return SyncResult{}, err
	}
	return s.sync(ctx, scope, batchTimeout, func(editor Editor, result *SyncResult) error {
		return applyDeltas(editor, result, changes)
	})
}

func collectDeltas(ctx context.Context, receive func() (UsersChunk, error)) (Scope, []accountDelta, error) {
	var scope Scope
	var changes []accountDelta
	names := make(map[string]struct{})
	ids := make(map[IDKey]struct{})
	var last bool
	for index := uint32(0); ; index++ {
		if err := ctx.Err(); err != nil {
			return Scope{}, nil, err
		}
		chunk, err := receive()
		if errors.Is(err, io.EOF) && last {
			return scope, changes, ctx.Err()
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return Scope{}, nil, err
		}
		if err != nil || last || index >= maxChunks || chunk.Index != index ||
			len(chunk.Accounts) > MaxChunkAccounts || len(chunk.Accounts)+len(changes) > MaxSyncAccounts ||
			(len(chunk.Accounts) == 0 && !chunk.Last) {
			return Scope{}, nil, ErrInvalid
		}
		if index == 0 {
			scope = chunk.Scope
			if !validScope(scope) {
				return Scope{}, nil, ErrInvalid
			}
		} else if scope != chunk.Scope {
			return Scope{}, nil, ErrInvalid
		}
		for _, input := range chunk.Accounts {
			if !validName(input.Email, 1024) {
				return Scope{}, nil, ErrInvalid
			}
			name := NameKey(input.Email)
			if _, exists := names[name]; exists {
				return Scope{}, nil, ErrInvalid
			}
			names[name] = struct{}{}
			change := accountDelta{account: Account{Email: input.Email}}
			if input.UUID != nil {
				id, valid := ParseUUID(*input.UUID)
				if !valid {
					return Scope{}, nil, ErrInvalid
				}
				key := id.Key()
				if _, exists := ids[key]; exists {
					return Scope{}, nil, ErrInvalid
				}
				ids[key] = struct{}{}
				change.account.ID, change.key, change.present = id, key, true
			}
			changes = append(changes, change)
		}
		last = chunk.Last
	}
}

func applyDeltas(editor Editor, result *SyncResult, changes []accountDelta) error {
	var removals []string
	released := make(map[IDKey]struct{})
	var additions []accountDelta
	for _, change := range changes {
		email := change.account.Email
		current, exists := editor.Lookup(email)
		if change.present && exists && current == change.key {
			continue
		}
		if exists {
			removals = append(removals, email)
			released[current] = struct{}{}
		}
		if change.present {
			additions = append(additions, change)
		}
	}
	if len(additions)-len(removals) > editor.Capacity() {
		return ErrTooLarge
	}
	accounts := make([]Account, 0, len(additions))
	for _, change := range additions {
		if _, free := released[change.key]; !free && editor.HasID(change.key) {
			return ErrExists
		}
		accounts = append(accounts, change.account)
	}
	return applyPlan(editor, result, removals, accounts)
}
