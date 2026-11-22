package api

import (
	"context"
	"errors"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/rpc"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	handlers "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func (c Client) Add(ctx context.Context, tag string, content []byte) (domain.NativeResult, error) {
	if err := ctx.Err(); err != nil {
		return domain.NativeResult{Effect: domain.NotApplied}, err
	}
	prepared, err := c.Prepare(content)
	if err != nil {
		return domain.NativeResult{Effect: domain.NotApplied}, err
	}
	return prepared.Add(ctx, tag)
}

// Prepare decodes untrusted wire input once for compatibility and submission.
func (c Client) Prepare(content []byte) (domain.PreparedAccount, error) {
	var user protocol.User
	if err := proto.Unmarshal(content, &user); err != nil {
		return nil, errors.Join(domain.ErrInvalidArgument, err)
	}
	return c.PrepareNative(&user)
}

type preparedAccount struct {
	client Client
	user   *protocol.User
}

// PrepareNative takes ownership of a decoded user without submitting a change.
func (c Client) PrepareNative(user *protocol.User) (domain.PreparedAccount, error) {
	if user == nil || user.Account == nil || user.Account.Type == "" || len(user.Account.Type) > 1024 {
		return nil, domain.ErrInvalidArgument
	}
	if err := validators.Email(user.Email); err != nil {
		return nil, err
	}
	return preparedAccount{client: c, user: user}, nil
}
func (a preparedAccount) AccountType() string { return a.user.Account.Type }
func (a preparedAccount) Add(ctx context.Context, tag string) (domain.NativeResult, error) {
	if err := ctx.Err(); err != nil {
		return domain.NativeResult{Email: a.user.Email, Effect: domain.NotApplied}, err
	}
	result, err := a.client.alter(ctx, tag, &handlers.AddUserOperation{User: a.user})
	result.Email = a.user.Email
	return result, err
}

func (c Client) Remove(ctx context.Context, tag, email string) (domain.NativeResult, error) {
	if err := ctx.Err(); err != nil {
		return domain.NativeResult{Effect: domain.NotApplied}, err
	}
	return c.alter(ctx, tag, &handlers.RemoveUserOperation{Email: email})
}
func (c Client) alter(ctx context.Context, tag string, operation proto.Message) (domain.NativeResult, error) {
	content, err := proto.Marshal(operation)
	if err != nil {
		return domain.NativeResult{Effect: domain.NotApplied}, errors.Join(domain.ErrInvalidArgument, err)
	}
	var remote peer.Peer
	_, err = c.native.AlterInbound(ctx, &handlers.AlterInboundRequest{Tag: tag, Operation: &serial.TypedMessage{Type: serial.GetMessageType(operation), Value: content}}, grpc.Peer(&remote))
	if err == nil {
		return domain.NativeResult{Effect: domain.Completed, Acknowledged: true}, nil
	}
	if errors.Is(err, rpc.ErrClosed) || status.Code(err) == codes.Unimplemented {
		return domain.NativeResult{Effect: domain.NotApplied}, errors.Join(domain.ErrUnsupported, err)
	}
	if remote.Addr == nil {
		return domain.NativeResult{Effect: domain.NotApplied}, errors.Join(err, ctx.Err())
	}
	// A terminal native error is not proof of rollback. Missing acknowledgements
	// may leave native work in flight, requiring explicit execution replacement.
	state, terminal := status.FromError(err)
	return domain.NativeResult{Effect: domain.Unknown, RequiresInstanceRestart: ctx.Err() != nil || !terminal || state.Code() != codes.Unknown}, errors.Join(err, ctx.Err())
}
