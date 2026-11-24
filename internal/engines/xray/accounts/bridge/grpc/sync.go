package grpc

import (
	"context"
	"time"

	accountsv1 "github.com/PapilioVPN/node/internal/engines/xray/accounts/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/xray/accounts/membership"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func (h *handlers) SyncUser(ctx context.Context, req *accountsv1.SyncUserRequest) (*accountsv1.SyncResult, error) {
	request, err := userRequest(req)
	if err != nil {
		return nil, accountError(err)
	}
	result, err := h.service.SyncUser(ctx, request)
	return syncResponse(result, err)
}

func userRequest(req *accountsv1.SyncUserRequest) (membership.SyncUserRequest, error) {
	input, err := decodeAccount(req.GetEmail(), req.Uuid, req.Flow, req.GetCredential(), req.GetAssignments())
	return membership.SyncUserRequest{
		Scope:       membership.Scope{InstanceID: req.GetId(), InboundTag: req.GetInboundTag()},
		InboundTags: req.InboundTags, Email: input.Email, UUID: input.UUID, Flow: input.Flow,
		Credential: input.Credential, Assignments: input.Assignments,
	}, err
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
		Scope: membership.Scope{InstanceID: req.GetId(), InboundTag: req.GetInboundTag()}, InboundTags: req.InboundTags, Accounts: accounts,
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
		input, err := decodeAccount(account.GetEmail(), account.Uuid, account.Flow, account.GetCredential(), account.GetAssignments())
		if err != nil {
			return nil, err
		}
		result[i] = input
	}
	return result, nil
}

const maxStreamBytes = 2 << 20

func (h *handlers) SyncUserStream(stream accountsv1.XrayAccountsService_SyncUserStreamServer) error {
	ctx := stream.Context()
	if err := streamDeadline(ctx); err != nil {
		return err
	}
	var bytes int
	result, err := h.service.SyncUserStream(ctx, func() (membership.SyncUserRequest, error) {
		req, err := stream.Recv()
		if err != nil {
			return membership.SyncUserRequest{}, err
		}
		bytes += proto.Size(req)
		if bytes > maxStreamBytes {
			return membership.SyncUserRequest{}, errStreamSize()
		}
		return userRequest(req)
	})
	return sendStreamResult(stream.SendAndClose, result, err)
}

func (h *handlers) SyncUsersChunked(stream accountsv1.XrayAccountsService_SyncUsersChunkedServer) error {
	ctx := stream.Context()
	if err := streamDeadline(ctx); err != nil {
		return err
	}
	var bytes int
	result, err := h.service.SyncUsersChunked(ctx, func() (membership.UsersChunk, error) {
		req, err := stream.Recv()
		if err != nil {
			return membership.UsersChunk{}, err
		}
		bytes += proto.Size(req)
		if bytes > maxStreamBytes {
			return membership.UsersChunk{}, errStreamSize()
		}
		if len(req.GetAccounts()) > membership.MaxChunkAccounts {
			return membership.UsersChunk{}, membership.ErrInvalid
		}
		accounts, err := accountInputs(req.GetAccounts())
		return membership.UsersChunk{
			Scope:       membership.Scope{InstanceID: req.GetId(), InboundTag: req.GetInboundTag()},
			InboundTags: req.InboundTags, Index: req.GetIndex(), Last: req.GetLast(), Accounts: accounts,
		}, err
	})
	return sendStreamResult(stream.SendAndClose, result, err)
}

func streamDeadline(ctx context.Context) error {
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > membership.StreamTimeout {
		return status.Error(codes.InvalidArgument, "account stream requires a deadline of at most 30 seconds")
	}
	return nil
}

func errStreamSize() error {
	return status.Error(codes.ResourceExhausted, "Xray account stream exceeds the supported size")
}

func sendStreamResult(send func(*accountsv1.SyncResult) error, result membership.SyncResult, err error) error {
	switch status.Code(err) {
	case codes.Canceled:
		return status.FromContextError(context.Canceled).Err()
	case codes.DeadlineExceeded:
		return status.FromContextError(context.DeadlineExceeded).Err()
	case codes.ResourceExhausted:
		return errStreamSize()
	}
	response, err := syncResponse(result, err)
	if err != nil {
		return err
	}
	return send(response)
}

// DecodeCredential adapts the versioned credential for account membership operations.
func DecodeCredential(input *accountsv1.Credential) (*membership.Credential, error) {
	if input == nil {
		return nil, nil
	}
	value := &membership.Credential{
		Protocol: membership.Protocol(input.Protocol), UUID: input.Uuid, Flow: input.Flow,
		Password: input.Password, Method: input.Method, Key: input.Key, Auth: input.Auth,
	}
	if peer := input.GetPeer(); peer != nil {
		if peer.KeepAlive > 65535 {
			return nil, membership.ErrInvalid
		}
		value.Peer = &membership.Peer{
			PublicKey: peer.PublicKey, PreSharedKey: peer.PreSharedKey, AllowedIPs: peer.AllowedIps,
			KeepAlive: uint16(peer.KeepAlive),
		}
	}
	return value, nil
}

func decodeAccount(email string, uuid, flow *string, credential *accountsv1.Credential, assignments []*accountsv1.Assignment) (membership.AccountInput, error) {
	input := membership.AccountInput{Email: email, UUID: uuid, Flow: flow}
	var err error
	input.Credential, err = DecodeCredential(credential)
	if err != nil || len(assignments) > membership.MaxSyncInbounds {
		return input, membership.ErrInvalid
	}
	for _, assignment := range assignments {
		value, err := DecodeCredential(assignment.GetCredential())
		if err != nil || value == nil {
			return input, membership.ErrInvalid
		}
		input.Assignments = append(input.Assignments, membership.Assignment{InboundTag: assignment.GetInboundTag(), Credential: *value})
	}
	return input, nil
}
