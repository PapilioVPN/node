package grpc

import (
	"context"
	"errors"

	outboundsv1 "github.com/PapilioVPN/node/internal/engines/xray/outbounds/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/xray/outbounds/health"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type handlers struct {
	outboundsv1.UnimplementedXrayOutboundsServiceServer
	service *health.Service
}

func Register(server grpc.ServiceRegistrar, service *health.Service) {
	outboundsv1.RegisterXrayOutboundsServiceServer(server, &handlers{service: service})
}

func (h *handlers) GetOutboundHealth(ctx context.Context, req *outboundsv1.XrayOutboundHealthRequest) (*outboundsv1.XrayOutboundHealth, error) {
	samples, err := h.service.Get(ctx, health.Request{InstanceID: req.GetId(), Tag: req.Tag})
	if err != nil {
		return nil, readError(err)
	}
	result := &outboundsv1.XrayOutboundHealth{Observations: make([]*outboundsv1.XrayOutboundObservation, len(samples))}
	for i, sample := range samples {
		result.Observations[i] = &outboundsv1.XrayOutboundObservation{
			Tag:                 sample.Tag,
			Status:              sampleStatus(sample.Status),
			DelayMilliseconds:   sample.DelayMilliseconds,
			LastSeenUnixSeconds: sample.LastSeenUnixSeconds,
			LastTryUnixSeconds:  sample.LastTryUnixSeconds,
		}
	}
	return result, nil
}

func sampleStatus(value health.Status) outboundsv1.XrayOutboundHealthStatus {
	switch value {
	case health.Healthy:
		return outboundsv1.XrayOutboundHealthStatus_XRAY_OUTBOUND_HEALTH_STATUS_HEALTHY
	case health.Failed:
		return outboundsv1.XrayOutboundHealthStatus_XRAY_OUTBOUND_HEALTH_STATUS_FAILED
	case health.NoSample:
		return outboundsv1.XrayOutboundHealthStatus_XRAY_OUTBOUND_HEALTH_STATUS_NO_SAMPLE
	default:
		return outboundsv1.XrayOutboundHealthStatus_XRAY_OUTBOUND_HEALTH_STATUS_UNSPECIFIED
	}
}

func readError(err error) error {
	switch {
	case errors.Is(err, health.ErrInvalid):
		return status.Error(codes.InvalidArgument, health.ErrInvalid.Error())
	case errors.Is(err, health.ErrNotFound):
		return status.Error(codes.NotFound, health.ErrNotFound.Error())
	case errors.Is(err, health.ErrDisabled):
		return status.Error(codes.FailedPrecondition, health.ErrDisabled.Error())
	case errors.Is(err, health.ErrBusy), errors.Is(err, health.ErrTooLarge):
		return status.Error(codes.ResourceExhausted, err.Error())
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return status.FromContextError(err).Err()
	default:
		return status.Error(codes.Unavailable, health.ErrUnavailable.Error())
	}
}
