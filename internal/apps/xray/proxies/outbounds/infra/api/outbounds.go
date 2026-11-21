// Package api observes outbound handlers through the pinned native SDK.
package api

import (
	"context"
	"errors"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/rpc"
	handlers "github.com/xtls/xray-core/app/proxyman/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type Client struct{ native handlers.HandlerServiceClient }

func New(connection grpc.ClientConnInterface) Client {
	return Client{handlers.NewHandlerServiceClient(connection)}
}
func (c Client) Records(ctx context.Context) ([]domain.Record, error) {
	result, err := c.native.ListOutbounds(ctx, &handlers.ListOutboundsRequest{}, grpc.MaxCallRecvMsgSize(domain.MaxResponseBytes))
	if err != nil {
		switch {
		case errors.Is(err, rpc.ErrClosed), status.Code(err) == codes.Unimplemented:
			err = errors.Join(domain.ErrUnsupported, err)
		case status.Code(err) == codes.ResourceExhausted:
			err = errors.Join(domain.ErrCapacity, err)
		}
		return nil, errors.Join(err, ctx.Err())
	}
	if result == nil || len(result.Outbounds) > domain.MaxRecords {
		return nil, domain.ErrCapacity
	}
	records := make([]domain.Record, len(result.Outbounds))
	for i, configuration := range result.Outbounds {
		if configuration == nil {
			return nil, domain.ErrInvalidArgument
		}
		content, err := proto.Marshal(configuration)
		if err != nil {
			return nil, err
		}
		records[i] = domain.Record{Tag: configuration.Tag, Configuration: content}
	}
	return records, ctx.Err()
}
