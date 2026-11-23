package grpc

import (
	"context"

	accountsv1 "github.com/PapilioVPN/node/internal/engines/xray/accounts/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/xray/accounts/membership"
)

func (h *handlers) SyncUser(ctx context.Context, req *accountsv1.SyncUserRequest) (*accountsv1.SyncResult, error) {
	result, err := h.service.SyncUser(ctx, membership.SyncUserRequest{
		Scope: membership.Scope{InstanceID: req.GetId(), InboundTag: req.GetInboundTag()},
		Email: req.GetEmail(), UUID: req.Uuid,
	})
	return syncResponse(result, err)
}

func syncResponse(result membership.SyncResult, err error) (*accountsv1.SyncResult, error) {
	if err != nil {
		return nil, accountError(err)
	}
	return &accountsv1.SyncResult{
		Outcome: string(result.Outcome), Added: result.Added, Removed: result.Removed, Failure: result.Failure,
	}, nil
}

func (h *handlers) SyncUsers(ctx context.Context, req *accountsv1.SyncUsersRequest) (*accountsv1.SyncResult, error) {
	if req.GetSnapshot() == nil {
		return nil, accountError(membership.ErrInvalid)
	}
	accounts, err := accountInputs(req.GetSnapshot().GetAccounts())
	if err != nil {
		return nil, accountError(err)
	}
	result, err := h.service.SyncUsers(ctx, membership.SyncUsersRequest{
		Scope: membership.Scope{InstanceID: req.GetId(), InboundTag: req.GetInboundTag()}, Accounts: accounts,
	})
	return syncResponse(result, err)
}

func accountInputs(accounts []*accountsv1.VLESSAccount) ([]membership.AccountInput, error) {
	if len(accounts) > membership.MaxSyncAccounts {
		return nil, membership.ErrInvalid
	}
	result := make([]membership.AccountInput, len(accounts))
	for i, account := range accounts {
		if account == nil {
			return nil, membership.ErrInvalid
		}
		result[i] = membership.AccountInput{Email: account.GetEmail(), UUID: account.Uuid}
	}
	return result, nil
}
