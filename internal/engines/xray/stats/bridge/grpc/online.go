package grpc

import (
	"context"
	"errors"

	"github.com/PapilioVPN/node/internal/engines/xray/instances/lifecycle"
	statsv1 "github.com/PapilioVPN/node/internal/engines/xray/stats/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/xray/stats/online"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (h *handlers) GetOnlineIPCount(ctx context.Context, req *statsv1.XrayOnlineRequest) (*statsv1.XrayOnlineCount, error) {
	count, err := h.online.Count(ctx, req.GetId(), req.GetName())
	if err != nil {
		return nil, onlineError(err)
	}
	return &statsv1.XrayOnlineCount{Name: req.GetName(), Count: count}, nil
}

func (h *handlers) ListOnlineIPs(ctx context.Context, req *statsv1.XrayOnlineRequest) (*statsv1.XrayOnlineIPs, error) {
	ips, err := h.online.IPs(ctx, req.GetId(), req.GetName())
	if err != nil {
		return nil, onlineError(err)
	}
	return &statsv1.XrayOnlineIPs{Name: req.GetName(), LastSeenUnixSeconds: ips}, nil
}

func (h *handlers) ListOnlineUsers(ctx context.Context, req *statsv1.XrayOnlineUsersRequest) (*statsv1.XrayOnlineUsers, error) {
	names, err := h.online.Users(ctx, req.GetId())
	if err != nil {
		return nil, onlineError(err)
	}
	return &statsv1.XrayOnlineUsers{Names: names}, nil
}

func onlineError(err error) error {
	switch {
	case errors.Is(err, lifecycle.ErrNotFound), errors.Is(err, online.ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, online.ErrInvalid):
		return status.Error(codes.InvalidArgument, online.ErrInvalid.Error())
	case errors.Is(err, online.ErrBusy), errors.Is(err, online.ErrTooLarge):
		return status.Error(codes.ResourceExhausted, err.Error())
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return status.FromContextError(err).Err()
	default:
		return status.Error(codes.Unavailable, online.ErrUnavailable.Error())
	}
}
