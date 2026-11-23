package grpc

import (
	"context"
	"errors"

	"github.com/PapilioVPN/node/internal/engines/xray/instances/lifecycle"
	statsv1 "github.com/PapilioVPN/node/internal/engines/xray/stats/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/xray/stats/traffic"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (h *handlers) GetTraffic(ctx context.Context, req *statsv1.XrayTrafficRequest) (*statsv1.XrayTrafficUsage, error) {
	result, err := h.traffic.Get(ctx, req.GetId(), trafficScope(req.GetScope()), req.GetName())
	if err != nil {
		return nil, trafficError(err)
	}
	return wireUsage(&result), nil
}

func (h *handlers) ListTraffic(ctx context.Context, req *statsv1.XrayTrafficListRequest) (*statsv1.XrayTrafficList, error) {
	results, err := h.traffic.List(ctx, req.GetId(), trafficScope(req.GetScope()))
	if err != nil {
		return nil, trafficError(err)
	}
	reply := &statsv1.XrayTrafficList{Entries: make([]*statsv1.XrayTrafficUsage, len(results))}
	for i := range results {
		reply.Entries[i] = wireUsage(&results[i])
	}
	return reply, nil
}

func trafficScope(scope statsv1.XrayTrafficScope) traffic.Scope {
	switch scope {
	case statsv1.XrayTrafficScope_XRAY_TRAFFIC_SCOPE_USER:
		return traffic.User
	case statsv1.XrayTrafficScope_XRAY_TRAFFIC_SCOPE_INBOUND:
		return traffic.Inbound
	case statsv1.XrayTrafficScope_XRAY_TRAFFIC_SCOPE_OUTBOUND:
		return traffic.Outbound
	default:
		return ""
	}
}

func wireUsage(entry *traffic.Usage) *statsv1.XrayTrafficUsage {
	reply := &statsv1.XrayTrafficUsage{Name: entry.Name}
	if entry.HasUplink {
		reply.UplinkBytes = &entry.UplinkBytes
	}
	if entry.HasDownlink {
		reply.DownlinkBytes = &entry.DownlinkBytes
	}
	return reply
}

func trafficError(err error) error {
	switch {
	case errors.Is(err, lifecycle.ErrNotFound):
		return status.Error(codes.NotFound, lifecycle.ErrNotFound.Error())
	case errors.Is(err, traffic.ErrNotFound):
		return status.Error(codes.NotFound, traffic.ErrNotFound.Error())
	case errors.Is(err, traffic.ErrInvalid):
		return status.Error(codes.InvalidArgument, traffic.ErrInvalid.Error())
	case errors.Is(err, traffic.ErrBusy):
		return status.Error(codes.ResourceExhausted, traffic.ErrBusy.Error())
	case errors.Is(err, traffic.ErrTooLarge):
		return status.Error(codes.ResourceExhausted, traffic.ErrTooLarge.Error())
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, "Xray traffic request canceled")
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, "Xray traffic request deadline exceeded")
	default:
		return status.Error(codes.Unavailable, "Xray traffic counters unavailable")
	}
}
