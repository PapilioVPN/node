package api

import (
	"context"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	handlers "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/common/protocol"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

func (c Client) collection(ctx context.Context, tag string) (*handlers.GetInboundUserResponse, domain.ReadCost, error) {
	started := time.Now()
	response, err := c.native.GetInboundUsers(ctx, &handlers.GetInboundUserRequest{Tag: tag}, grpc.MaxCallRecvMsgSize(domain.MaxCollectionBytes))
	if err != nil {
		return nil, domain.ReadCost{}, failure(ctx, err)
	}
	if response == nil {
		return nil, domain.ReadCost{}, domain.ErrInvalidResponse
	}
	if len(response.Users) > domain.MaxCollectionRecords {
		return nil, domain.ReadCost{}, domain.ErrCapacity
	}
	cost := domain.ReadCost{NativeBytes: proto.Size(response), NativeDuration: time.Since(started)}
	if cost.NativeBytes > domain.MaxCollectionBytes {
		return nil, cost, domain.ErrCapacity
	}
	return response, cost, ctx.Err()
}
func (c Client) Records(ctx context.Context, tag string) (domain.NativeRecords, error) {
	response, cost, err := c.collection(ctx, tag)
	if err != nil {
		return domain.NativeRecords{}, err
	}
	result := domain.NativeRecords{Users: make([]domain.Record, len(response.Users)), ReadCost: cost}
	for i, user := range response.Users {
		if err := ctx.Err(); err != nil {
			return domain.NativeRecords{}, err
		}
		if user == nil {
			return domain.NativeRecords{}, domain.ErrInvalidResponse
		}
		if proto.Size(user) > domain.MaxAccountBytes {
			return domain.NativeRecords{}, domain.ErrCapacity
		}
		result.Users[i], err = decode(user)
		if err != nil {
			return domain.NativeRecords{}, err
		}
	}
	return result, nil
}
func (c Client) Metadata(ctx context.Context, tag string) (domain.NativeMetadata, error) {
	response, cost, err := c.collection(ctx, tag)
	if err != nil {
		return domain.NativeMetadata{}, err
	}
	result := domain.NativeMetadata{Users: make([]domain.Metadata, len(response.Users)), ReadCost: cost}
	for i, user := range response.Users {
		if err := ctx.Err(); err != nil {
			return domain.NativeMetadata{}, err
		}
		result.Users[i], err = metadata(user)
		if err != nil {
			return domain.NativeMetadata{}, err
		}
	}
	return result, nil
}
func metadata(user *protocol.User) (domain.Metadata, error) {
	if user == nil || user.Account == nil || user.Account.Type == "" {
		return domain.Metadata{}, domain.ErrInvalidResponse
	}
	return domain.Metadata{Email: user.Email, Level: user.Level, AccountType: user.Account.Type}, nil
}
func (c Client) GetMetadata(ctx context.Context, tag, email string) (*domain.Metadata, error) {
	if email == "" {
		return nil, domain.ErrInvalidArgument
	}
	response, err := c.native.GetInboundUsers(ctx, &handlers.GetInboundUserRequest{Tag: tag, Email: email}, grpc.MaxCallRecvMsgSize(domain.MaxAccountBytes))
	if err != nil {
		return nil, failure(ctx, err)
	}
	if response == nil || len(response.Users) > 1 {
		return nil, domain.ErrInvalidResponse
	}
	if len(response.Users) == 0 {
		return nil, ctx.Err()
	}
	user := response.Users[0]
	if user != nil && user.Account == nil && user.Email == "" && user.Level == 0 && len(user.ProtoReflect().GetUnknown()) == 0 {
		return nil, ctx.Err()
	}
	result, err := metadata(user)
	if err != nil {
		return nil, err
	}
	return &result, ctx.Err()
}
