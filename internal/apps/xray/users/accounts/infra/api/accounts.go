// Package api uses focused native UserManager operations without preflight scans.
package api

import (
	"context"
	"errors"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/rpc"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	handlers "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/common/protocol"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type Client struct{ native handlers.HandlerServiceClient }

func New(connection grpc.ClientConnInterface) Client {
	return Client{handlers.NewHandlerServiceClient(connection)}
}
func (c Client) Count(ctx context.Context, tag string) (int64, error) {
	result, err := c.native.GetInboundUsersCount(ctx, &handlers.GetInboundUserRequest{Tag: tag})
	if err != nil {
		return 0, failure(ctx, err)
	}
	if result == nil || result.Count < 0 {
		return 0, domain.ErrInvalidResponse
	}
	return result.Count, ctx.Err()
}
func (c Client) Get(ctx context.Context, tag, email string) (*domain.Record, error) {
	// An empty email changes this native RPC into an unbounded collection request.
	if email == "" {
		return nil, domain.ErrInvalidArgument
	}
	result, err := c.native.GetInboundUsers(ctx, &handlers.GetInboundUserRequest{Tag: tag, Email: email}, grpc.MaxCallRecvMsgSize(domain.MaxAccountBytes))
	if err != nil {
		return nil, failure(ctx, err)
	}
	if result == nil || len(result.Users) > 1 {
		return nil, domain.ErrInvalidResponse
	}
	if len(result.Users) == 0 {
		return nil, ctx.Err()
	}
	user := result.Users[0]
	// Native ToProtoUser(nil) is serialized as an empty repeated-message element.
	if user == nil || (user.Account == nil && user.Email == "" && user.Level == 0 && len(user.ProtoReflect().GetUnknown()) == 0) {
		return nil, ctx.Err()
	}
	record, err := decode(user)
	if err != nil {
		return nil, err
	}
	return &record, ctx.Err()
}
func decode(user *protocol.User) (domain.Record, error) {
	if user == nil || user.Account == nil {
		return domain.Record{}, domain.ErrInvalidResponse
	}
	content, err := proto.Marshal(user)
	return domain.Record{Email: user.Email, Level: user.Level, Configuration: content}, err
}
func failure(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, rpc.ErrClosed), status.Code(err) == codes.Unimplemented:
		err = errors.Join(domain.ErrUnsupported, err)
	case status.Code(err) == codes.ResourceExhausted:
		err = errors.Join(domain.ErrCapacity, err)
	}
	return errors.Join(err, ctx.Err())
}
