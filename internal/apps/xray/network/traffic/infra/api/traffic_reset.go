package api

import (
	"context"
	"errors"
	"fmt"
	"time"

	native "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/apps/xray/network/traffic/domain"
)

// ResetTrafficCounter swaps one counter with zero in one native GetStats call.
// A valid acknowledgement is retained even if ctx is canceled just afterwards.
// After dispatch, missing/malformed/lost replies can mean the effect already ran.
func (c *Counters) ResetTrafficCounter(ctx context.Context, selection domain.TrafficSelection) (domain.TrafficCounterReset, error) {
	if err := ctx.Err(); err != nil {
		return domain.TrafficCounterReset{}, err
	}
	ctx, done, err := c.API.Begin(ctx)
	if err != nil {
		return domain.TrafficCounterReset{}, err
	}
	defer done()
	client, err := c.API.StatsClient()
	if err != nil {
		return domain.TrafficCounterReset{}, err
	}
	// Cancellation before invocation is known not to have changed the engine.
	if err := ctx.Err(); err != nil {
		return domain.TrafficCounterReset{}, err
	}
	name := trafficCounterName(selection)
	response, err := client.GetStats(ctx, &native.GetStatsRequest{Name: name, Reset_: true})
	if err != nil {
		var cause error
		switch status.Code(err) {
		case codes.NotFound:
			cause = domain.ErrTrafficCounterNotFound
		case codes.Unimplemented:
			cause = domain.ErrTrafficUnsupported
		case codes.ResourceExhausted:
			cause = errors.Join(domain.ErrTrafficResetUnknown, domain.ErrAPIResourceLimit)
		case codes.Canceled:
			cause = errors.Join(domain.ErrTrafficResetUnknown, context.Canceled)
		case codes.DeadlineExceeded:
			cause = errors.Join(domain.ErrTrafficResetUnknown, context.DeadlineExceeded)
		default:
			cause = domain.ErrTrafficResetUnknown
		}
		return domain.TrafficCounterReset{}, fmt.Errorf("reset Xray traffic counter: %w", errors.Join(cause, ctx.Err(), err))
	}
	value := response.GetStat()
	if value == nil || value.Name != name {
		return domain.TrafficCounterReset{}, errors.Join(domain.ErrTrafficResetUnknown, domain.ErrInvalidTrafficResponse, ctx.Err())
	}
	return domain.TrafficCounterReset{PreviousBytes: value.Value, AcknowledgedAt: time.Now().UTC()}, nil
}
