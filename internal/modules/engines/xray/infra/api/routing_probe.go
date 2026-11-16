package api

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
)

// TestRoute invokes native selection once. Selection, DNS, webhook and publication
// effects are engine-owned and may outlive RPC cancellation. Never replay a probe.
func (c *Adapter) TestRoute(ctx context.Context, probe domain.RoutingProbe) (domain.RoutingProbeResult, error) {
	if err := ctx.Err(); err != nil {
		return domain.RoutingProbeResult{}, err
	}
	ctx, done, err := c.API.Begin(ctx)
	if err != nil {
		return domain.RoutingProbeResult{}, err
	}
	defer done()
	request, err := encodeRoutingProbe(probe)
	if err != nil {
		return domain.RoutingProbeResult{}, err
	}
	client, err := c.API.RoutingClient()
	if err != nil {
		return domain.RoutingProbeResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.RoutingProbeResult{}, err
	}
	response, err := client.TestRoute(ctx, request)
	if err != nil {
		return domain.RoutingProbeResult{}, fmt.Errorf("test Xray route: %w", routingProbeCause(ctx, err))
	}
	route, err := decodeRoutingResult(response)
	if err != nil {
		return domain.RoutingProbeResult{}, errors.Join(domain.ErrRoutingProbeUnknown, ctx.Err(), err)
	}
	// Retain a valid acknowledgement even if cancellation races after receipt.
	return domain.RoutingProbeResult{Route: route, AcknowledgedAt: time.Now().UTC()}, nil
}

func routingProbeCause(ctx context.Context, err error) error {
	cause := domain.ErrRoutingProbeUnknown
	switch status.Code(err) {
	case codes.Unimplemented:
		cause = domain.ErrRoutingProbeUnsupported
	case codes.ResourceExhausted:
		cause = errors.Join(cause, domain.ErrAPIResourceLimit)
	case codes.Canceled:
		cause = errors.Join(cause, context.Canceled)
	case codes.DeadlineExceeded:
		cause = errors.Join(cause, context.DeadlineExceeded)
	}
	return errors.Join(cause, ctx.Err(), err)
}
