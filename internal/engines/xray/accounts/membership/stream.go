package membership

import (
	"context"
	"errors"
	"io"
)

// SyncUserStream reconciles the named users of one scope after a clean half-close.
func (s *Service) SyncUserStream(ctx context.Context, receive func() (SyncUserRequest, error)) (SyncResult, error) {
	select {
	case s.streams <- struct{}{}:
		defer func() { <-s.streams }()
	default:
		return SyncResult{Outcome: NotApplied, Failure: syncFailure(ErrBusy)}, nil
	}
	var scope Scope
	var tags []string
	accounts := make([]AccountInput, 0)
	for {
		if err := ctx.Err(); err != nil {
			return SyncResult{}, err
		}
		req, err := receive()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return SyncResult{}, err
		}
		if len(accounts) == MaxSyncAccounts {
			return SyncResult{}, ErrInvalid
		}
		if len(accounts) == 0 {
			scope, tags = req.Scope, req.InboundTags
			if _, err := selectInbounds(scope, tags); err != nil {
				return SyncResult{}, err
			}
		} else if req.Scope != scope || !sameTags(tags, req.InboundTags) {
			return SyncResult{}, ErrInvalid
		}
		accounts = append(accounts, req.account())
	}
	if err := ctx.Err(); err != nil {
		return SyncResult{}, err
	}
	if len(accounts) == 0 {
		return SyncResult{}, ErrInvalid
	}
	return s.reconcile(ctx, scope, tags, batchTimeout, accounts, false)
}
