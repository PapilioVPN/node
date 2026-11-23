package grpc

import (
	"context"
	"errors"

	statsv1 "github.com/PapilioVPN/node/internal/engines/xray/stats/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/xray/stats/traffic"
)

func (h *handlers) ResetTrafficScope(ctx context.Context, req *statsv1.XrayTrafficScopeResetRequest) (*statsv1.XrayTrafficScopeResetResult, error) {
	report, err := h.traffic.ResetScope(ctx, traffic.ScopeReset{
		ID: req.GetId(), Scope: trafficScope(req.GetScope()), MaxCounters: req.GetMaxCounters(),
	})
	if err != nil && report == nil {
		return nil, resetError(err)
	}
	outcome := statsv1.XrayTrafficScopeResetResult_COMPLETE
	switch {
	case errors.Is(err, traffic.ErrResetUnknown):
		outcome = statsv1.XrayTrafficScopeResetResult_UNKNOWN
	case err != nil:
		outcome = statsv1.XrayTrafficScopeResetResult_INTERRUPTED
	}
	// A full report can exceed the gRPC trailer limit. A formed report travels
	// in the response body with an explicit outcome, even after an interruption.
	reply := &statsv1.XrayTrafficScopeResetResult{
		Outcome: outcome, Counters: make([]*statsv1.XrayTrafficScopedCounterReset, len(report)),
	}
	rows := make([]statsv1.XrayTrafficScopedCounterReset, len(report))
	for i, row := range report {
		direction := statsv1.XrayTrafficDirection_XRAY_TRAFFIC_DIRECTION_DOWNLINK
		if row.Direction == traffic.Uplink {
			direction = statsv1.XrayTrafficDirection_XRAY_TRAFFIC_DIRECTION_UPLINK
		}
		rows[i] = statsv1.XrayTrafficScopedCounterReset{
			Name: row.Name, Direction: direction, Result: wireDirectionReset(row.DirectionReset),
		}
		reply.Counters[i] = &rows[i]
	}
	return reply, nil
}
