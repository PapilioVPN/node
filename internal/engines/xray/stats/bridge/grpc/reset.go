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
	if errors.Is(err, traffic.ErrResetUnknown) {
		effect = statsv1.XrayTrafficResetFailure_UNKNOWN
	}
	result := resetStatus(err)
	withEffect, detailErr := result.WithDetails(&statsv1.XrayTrafficResetFailure{Effect: effect})
	if detailErr != nil {
		return result.Err()
	}
	return withEffect.Err()
}

func resetStatus(err error) *status.Status {
	switch {
	case errors.Is(err, traffic.ErrResetUnknown):
		return status.New(codes.Unknown, traffic.ErrResetUnknown.Error())
	case errors.Is(err, traffic.ErrResetDisabled):
		return status.New(codes.PermissionDenied, traffic.ErrResetDisabled.Error())
	case errors.Is(err, traffic.ErrResetBusy):
		return status.New(codes.ResourceExhausted, traffic.ErrResetBusy.Error())
	default:
		return status.Convert(trafficError(err))
	}
}

func (h *handlers) ResetTrafficPair(ctx context.Context, req *statsv1.XrayTrafficPairResetRequest) (*statsv1.XrayTrafficPairResetResult, error) {
	result, err := h.traffic.ResetPair(ctx, traffic.CounterPair{
		ID: req.GetId(), Scope: trafficScope(req.GetScope()), Name: req.GetName(),
	})
	if err != nil && result.Uplink.Effect == "" {
		return nil, resetError(err)
	}
	reply := &statsv1.XrayTrafficPairResetResult{
		Uplink: wireDirectionReset(result.Uplink), Downlink: wireDirectionReset(result.Downlink),
	}
	if err == nil {
		return reply, nil
	}
	failure := status.New(codes.Aborted, "Xray traffic pair reset interrupted")
	if errors.Is(err, traffic.ErrResetUnknown) {
		failure = resetStatus(err)
	}
	withProgress, detailErr := failure.WithDetails(reply)
	if detailErr != nil {
		return nil, failure.Err()
	}
	return nil, withProgress.Err()
}

func wireDirectionReset(result traffic.DirectionReset) *statsv1.XrayTrafficDirectionReset {
	reply := new(statsv1.XrayTrafficDirectionReset)
	switch result.Effect {
	case traffic.ResetAcknowledged:
		reply.Effect = statsv1.XrayTrafficDirectionReset_ACKNOWLEDGED
		reply.PreviousBytes = &result.PreviousBytes
	case traffic.ResetMissing:
		reply.Effect = statsv1.XrayTrafficDirectionReset_MISSING
	case traffic.ResetNotApplied:
		reply.Effect = statsv1.XrayTrafficDirectionReset_NOT_APPLIED
	case traffic.ResetUnknown:
		reply.Effect = statsv1.XrayTrafficDirectionReset_UNKNOWN
	case traffic.ResetNotAttempted:
		reply.Effect = statsv1.XrayTrafficDirectionReset_NOT_ATTEMPTED
	}
	return reply
}
