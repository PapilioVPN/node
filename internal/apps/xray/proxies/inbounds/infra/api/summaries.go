package api

import (
	"context"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/infra/configuration"
	handlers "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/core"
	"google.golang.org/grpc"
)

// Native ListInbounds has no credential-free type or targeted projection. These
// operations each fetch once; they neither marshal the credential payloads into
// extra whole-record byte slices nor retain them in the projected result.
func (c Client) summarySource(ctx context.Context) ([]*core.InboundHandlerConfig, error) {
	response, err := c.native.ListInbounds(ctx, &handlers.ListInboundsRequest{}, grpc.MaxCallRecvMsgSize(domain.MaxResponseBytes))
	if err != nil {
		return nil, readError(ctx, err)
	}
	if response == nil || len(response.Inbounds) > domain.MaxRecords {
		return nil, domain.ErrCapacity
	}
	return response.Inbounds, nil
}
func (c Client) Summaries(ctx context.Context) ([]domain.Summary, error) {
	source, err := c.summarySource(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]domain.Summary, len(source))
	for i, item := range source {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		result[i], err = (configuration.Inspector{}).DescribeNative(item)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}
func (c Client) Compatibility(ctx context.Context, tag string) (domain.Summary, error) {
	source, err := c.summarySource(ctx)
	if err != nil {
		return domain.Summary{}, err
	}
	var selected *core.InboundHandlerConfig
	for _, item := range source {
		if item == nil {
			return domain.Summary{}, domain.ErrInvalidArgument
		}
		if item.Tag == tag {
			if selected != nil {
				return domain.Summary{}, domain.ErrAmbiguousInbound
			}
			selected = item
		}
	}
	if selected == nil {
		return domain.Summary{}, domain.ErrInboundNotFound
	}
	return (configuration.Inspector{}).DescribeNative(selected)
}
