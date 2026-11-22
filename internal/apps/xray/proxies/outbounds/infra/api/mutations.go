package api

import (
	"context"
	"errors"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/rpc"
	handlers "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/core"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func (c Client) Add(ctx context.Context, content []byte) (domain.NativeResult, error) {
	if err := ctx.Err(); err != nil {
		return domain.NativeResult{Effect: domain.NotApplied}, err
	}
	var configuration core.OutboundHandlerConfig
	if err := proto.Unmarshal(content, &configuration); err != nil {
		return domain.NativeResult{Effect: domain.NotApplied}, errors.Join(domain.ErrInvalidArgument, err)
	}
	prepared, err := c.PrepareNative(&configuration)
	if err != nil {
		return domain.NativeResult{Effect: domain.NotApplied}, err
	}
	return prepared.Add(ctx)
}

type addition struct {
	client        Client
	configuration *core.OutboundHandlerConfig
}

// PrepareNative takes ownership of a decoded handler without submitting it.
func (c Client) PrepareNative(configuration *core.OutboundHandlerConfig) (domain.Addition, error) {
	if configuration == nil || configuration.ProxySettings == nil || configuration.ProxySettings.Type == "" || (configuration.SenderSettings != nil && configuration.SenderSettings.Type == "") {
		return nil, domain.ErrInvalidArgument
	}
	if configuration.Tag != "" {
		if err := validators.Tag(configuration.Tag); err != nil {
			return nil, err
		}
	}
	return addition{client: c, configuration: configuration}, nil
}
func (a addition) Add(ctx context.Context) (domain.NativeResult, error) {
	if err := ctx.Err(); err != nil {
		return domain.NativeResult{Tag: a.configuration.Tag, Effect: domain.NotApplied}, err
	}
	var remote peer.Peer
	_, err := a.client.native.AddOutbound(ctx, &handlers.AddOutboundRequest{Outbound: a.configuration}, grpc.Peer(&remote))
	result, cause := outcome(ctx, err, remote)
	result.Tag = a.configuration.Tag
	return result, cause
}

func (c Client) Remove(ctx context.Context, tag string) (domain.NativeResult, error) {
	if err := ctx.Err(); err != nil {
		return domain.NativeResult{Effect: domain.NotApplied}, err
	}
	var remote peer.Peer
	_, err := c.native.RemoveOutbound(ctx, &handlers.RemoveOutboundRequest{Tag: tag}, grpc.Peer(&remote))
	return outcome(ctx, err, remote)
}
func outcome(ctx context.Context, err error, remote peer.Peer) (domain.NativeResult, error) {
	if err == nil {
		return domain.NativeResult{Effect: domain.Completed, Acknowledged: true}, nil
	}
	if errors.Is(err, rpc.ErrClosed) || status.Code(err) == codes.Unimplemented {
		return domain.NativeResult{Effect: domain.NotApplied}, errors.Join(domain.ErrUnsupported, err)
	}
	if remote.Addr == nil {
		return domain.NativeResult{Effect: domain.NotApplied}, errors.Join(err, ctx.Err())
	}
	// The pinned handlers return ordinary native errors as terminal Unknown status.
	// Such a response is not proof of rollback; a lost dispatched call may persist.
	state, terminal := status.FromError(err)
	return domain.NativeResult{Effect: domain.Unknown, RequiresInstanceRestart: ctx.Err() != nil || !terminal || state.Code() != codes.Unknown}, errors.Join(err, ctx.Err())
}
