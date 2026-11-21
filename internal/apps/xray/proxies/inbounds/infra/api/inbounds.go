// Package api implements live inbound operations through the pinned native SDK.
package api

import (
	"context"
	"errors"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/rpc"
	handlers "github.com/xtls/xray-core/app/proxyman/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type Client struct{ native handlers.HandlerServiceClient }

func New(connection grpc.ClientConnInterface) Client {
	return Client{native: handlers.NewHandlerServiceClient(connection)}
}
func (c Client) Records(ctx context.Context) ([]domain.Record, error) {
	result, err := c.native.ListInbounds(ctx, &handlers.ListInboundsRequest{}, grpc.MaxCallRecvMsgSize(domain.MaxResponseBytes))
	if err != nil {
		return nil, readError(ctx, err)
	}
	if result == nil || len(result.Inbounds) > domain.MaxRecords {
		return nil, domain.ErrCapacity
	}
	records := make([]domain.Record, len(result.Inbounds))
	for i, configuration := range result.Inbounds {
		if configuration == nil {
			return nil, domain.ErrInvalidArgument
		}
		content, err := proto.Marshal(configuration)
		if err != nil {
			return nil, err
		}
		records[i] = domain.Record{Tag: configuration.Tag, Configuration: content}
	}
	return records, nil
}
func (c Client) Tags(ctx context.Context) ([]string, error) {
	result, err := c.native.ListInbounds(ctx, &handlers.ListInboundsRequest{IsOnlyTags: true}, grpc.MaxCallRecvMsgSize(domain.MaxResponseBytes))
	if err != nil {
		return nil, readError(ctx, err)
	}
	if result == nil || len(result.Inbounds) > domain.MaxRecords {
		return nil, domain.ErrCapacity
	}
	tags := make([]string, len(result.Inbounds))
	for i, configuration := range result.Inbounds {
		if configuration == nil {
			return nil, domain.ErrInvalidArgument
		}
		tags[i] = configuration.Tag
	}
	return tags, nil
}
func readError(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, rpc.ErrClosed), status.Code(err) == codes.Unimplemented:
		err = errors.Join(domain.ErrUnsupported, err)
	case status.Code(err) == codes.ResourceExhausted:
		err = errors.Join(domain.ErrCapacity, err)
	}
	return errors.Join(err, ctx.Err())
}
