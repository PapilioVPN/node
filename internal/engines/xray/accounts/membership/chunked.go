package membership

import (
	"context"
	"errors"
	"io"
	"slices"
	"time"
)

const (
	MaxChunkAccounts = 100
	maxChunks        = 100
	StreamTimeout    = 30 * time.Second
)

type UsersChunk struct {
	Scope       Scope
	InboundTags []string
	Index       uint32
	Last        bool
	Accounts    []AccountInput
}

// SyncUsersChunked applies explicit deltas after the final marker and clean EOF.
func (s *Service) SyncUsersChunked(ctx context.Context, receive func() (UsersChunk, error)) (SyncResult, error) {
	select {
	case s.streams <- struct{}{}:
		defer func() { <-s.streams }()
	default:
		return SyncResult{Outcome: NotApplied, Failure: syncFailure(ErrBusy)}, nil
	}

	scope, tags, accounts, err := collectDeltas(ctx, receive)
	if err != nil {
		return SyncResult{}, err
	}
	return s.reconcile(ctx, scope, tags, batchTimeout, accounts, false)
}

func collectDeltas(ctx context.Context, receive func() (UsersChunk, error)) (Scope, []string, []AccountInput, error) {
	var scope Scope
	var tags []string
	accounts := make([]AccountInput, 0)
	var last bool
	for index := uint32(0); ; index++ {
		if err := ctx.Err(); err != nil {
			return Scope{}, nil, nil, err
		}
		chunk, err := receive()
		if errors.Is(err, io.EOF) && last {
			return scope, tags, accounts, ctx.Err()
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return Scope{}, nil, nil, err
		}
		if err != nil || last || index >= maxChunks || chunk.Index != index ||
			len(chunk.Accounts) > MaxChunkAccounts || len(chunk.Accounts)+len(accounts) > MaxSyncAccounts ||
			(len(chunk.Accounts) == 0 && !chunk.Last) {
			return Scope{}, nil, nil, ErrInvalid
		}
		if index == 0 {
			scope, tags = chunk.Scope, chunk.InboundTags
			if _, err := selectInbounds(scope, tags); err != nil {
				return Scope{}, nil, nil, err
			}
		} else if scope != chunk.Scope || !sameTags(tags, chunk.InboundTags) {
			return Scope{}, nil, nil, ErrInvalid
		}
		accounts = append(accounts, chunk.Accounts...)
		last = chunk.Last
	}
}

func sameTags(a, b []string) bool {
	return (a == nil) == (b == nil) && slices.Equal(a, b)
}
