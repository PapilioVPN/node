package api

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/xtls/xray-core/app/observatory"
	native "github.com/xtls/xray-core/app/observatory/command"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
)

// ReadOutboundStatuses reads existing native observations without starting probes.
func (c *Adapter) ReadOutboundStatuses(ctx context.Context) (domain.OutboundStatuses, error) {
	if err := ctx.Err(); err != nil {
		return domain.OutboundStatuses{}, err
	}
	ctx, done, err := c.API.Begin(ctx)
	if err != nil {
		return domain.OutboundStatuses{}, err
	}
	defer done()
	client, err := c.API.ObservatoryClient()
	if err != nil {
		return domain.OutboundStatuses{}, err
	}
	response, err := client.GetOutboundStatus(ctx, &native.GetOutboundStatusRequest{})
	if cause := ctx.Err(); cause != nil {
		return domain.OutboundStatuses{}, errors.Join(cause, err)
	}
	if err != nil {
		return domain.OutboundStatuses{}, outboundObservationError(err)
	}
	if response == nil || response.Status == nil {
		return domain.OutboundStatuses{}, domain.ErrInvalidOutboundObservation
	}
	if len(response.ProtoReflect().GetUnknown()) != 0 || len(response.Status.ProtoReflect().GetUnknown()) != 0 {
		return domain.OutboundStatuses{}, domain.ErrOutboundObservationUnsupported
	}
	result := domain.OutboundStatuses{Items: make([]domain.OutboundStatus, 0, len(response.Status.Status))}
	for _, value := range response.Status.Status {
		if err := ctx.Err(); err != nil {
			return domain.OutboundStatuses{}, err
		}
		item, err := observedOutbound(value)
		if err != nil {
			return domain.OutboundStatuses{}, err
		}
		result.Items = append(result.Items, item)
	}
	if err := ctx.Err(); err != nil {
		return domain.OutboundStatuses{}, err
	}
	result.ObservedAt = time.Now().UTC()
	return result, nil
}

func observedOutbound(value *observatory.OutboundStatus) (domain.OutboundStatus, error) {
	if value == nil {
		return domain.OutboundStatus{}, domain.ErrInvalidOutboundObservation
	}
	if len(value.ProtoReflect().GetUnknown()) != 0 {
		return domain.OutboundStatus{}, domain.ErrOutboundObservationUnsupported
	}
	result := domain.OutboundStatus{
		Tag: value.OutboundTag, Alive: value.Alive, DelayMS: value.Delay, LastErrorReason: value.LastErrorReason,
		LastSeenUnixSec: value.LastSeenTime, LastTryUnixSec: value.LastTryTime,
	}
	if health := value.HealthPing; health != nil {
		if len(health.ProtoReflect().GetUnknown()) != 0 {
			return domain.OutboundStatus{}, domain.ErrOutboundObservationUnsupported
		}
		result.HealthPing = &domain.OutboundHealthPing{
			TotalSamples: health.All, FailedSamples: health.Fail, DeviationNS: health.Deviation,
			AverageNS: health.Average, MaxNS: health.Max, MinNS: health.Min,
		}
	}
	return result, nil
}

func outboundObservationError(err error) error {
	var cause error
	switch status.Code(err) {
	case codes.Unimplemented:
		cause = domain.ErrOutboundObservationUnsupported
	case codes.ResourceExhausted:
		cause = domain.ErrAPIResourceLimit
	case codes.Canceled:
		cause = context.Canceled
	case codes.DeadlineExceeded:
		cause = context.DeadlineExceeded
	}
	return fmt.Errorf("read Xray outbound observations: %w", errors.Join(cause, err))
}
