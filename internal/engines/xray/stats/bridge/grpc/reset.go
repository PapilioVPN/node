package grpc

import (
	"context"
	"errors"

	statsv1 "github.com/PapilioVPN/node/internal/engines/xray/stats/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/xray/stats/traffic"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (h *handlers) ResetTrafficCounter(ctx context.Context, req *statsv1.XrayTrafficResetRequest) (*statsv1.XrayTrafficResetResult, error) {
	var direction traffic.Direction
	switch req.GetDirection() {
	case statsv1.XrayTrafficDirection_XRAY_TRAFFIC_DIRECTION_UPLINK:
		direction = traffic.Uplink
	case statsv1.XrayTrafficDirection_XRAY_TRAFFIC_DIRECTION_DOWNLINK:
		direction = traffic.Downlink
	}
	bytes, err := h.traffic.Reset(ctx, traffic.Counter{
		ID: req.GetId(), Scope: trafficScope(req.GetScope()), Name: req.GetName(), Direction: direction,
	})
	if err != nil {
		return nil, resetError(err)
	}
	return &statsv1.XrayTrafficResetResult{PreviousBytes: bytes}, nil
}

func resetError(err error) error {
	effect := statsv1.XrayTrafficResetFailure_NOT_APPLIED
	var result *status.Status
	switch {
	case errors.Is(err, traffic.ErrResetUnknown):
		effect = statsv1.XrayTrafficResetFailure_UNKNOWN
		result = status.New(codes.Unknown, traffic.ErrResetUnknown.Error())
	case errors.Is(err, traffic.ErrResetDisabled):
		result = status.New(codes.PermissionDenied, traffic.ErrResetDisabled.Error())
	case errors.Is(err, traffic.ErrResetBusy):
		result = status.New(codes.ResourceExhausted, traffic.ErrResetBusy.Error())
	default:
		result = status.Convert(trafficError(err))
	}
	withEffect, detailErr := result.WithDetails(&statsv1.XrayTrafficResetFailure{Effect: effect})
	if detailErr != nil {
		return result.Err()
	}
	return withEffect.Err()
}
