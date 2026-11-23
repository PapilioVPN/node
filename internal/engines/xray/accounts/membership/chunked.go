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
	return s.reconcile(ctx, scope, batchTimeout, changes, false)
}

func collectDeltas(ctx context.Context, receive func() (UsersChunk, error)) (Scope, []change, error) {
	var scope Scope
	var changes []change
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
		if len(chunk.Accounts) > 0 {
			items, err := changesFrom(chunk.Accounts, false)
			if err != nil {
				return Scope{}, nil, err
			}
			changes = append(changes, items...)
		}
		last = chunk.Last
	}
}
