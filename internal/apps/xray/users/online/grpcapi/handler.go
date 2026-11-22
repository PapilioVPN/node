// Package grpcapi exposes exact online reads through its versioned native service.
package grpcapi

import (
	"context"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/users/online/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/users/online/domain"
	onlinev1 "github.com/PapilioVPN/node/internal/apps/xray/users/online/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/apps/xray/users/online/infra/jsonwire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Handler struct {
	onlinev1.UnimplementedOnlineServiceServer
	Service *services.Service
	Timeout time.Duration
}

func (h Handler) Register(registrar grpc.ServiceRegistrar) {
	onlinev1.RegisterOnlineServiceServer(registrar, h)
}
func (h Handler) GetUserOnlineStats(ctx context.Context, r *onlinev1.GetUserOnlineStatsRequest) (*onlinev1.UserOnlineStats, error) {
	if r == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Service.Count(ctx, domain.Selection{InstanceID: r.InstanceId, Email: r.Email})
	if err != nil {
		return nil, failure(err)
	}
	return &onlinev1.UserOnlineStats{Observation: metadata(result.Observation), State: state(result.State()), Count: result.Count}, nil
}
func (h Handler) ListUserOnlineIPs(ctx context.Context, r *onlinev1.ListUserOnlineIPsRequest) (*onlinev1.UserOnlineIPs, error) {
	if r == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Service.PageIPs(ctx, domain.PageRequest{Selection: domain.Selection{InstanceID: r.InstanceId, Email: r.Email}, PageSize: int(r.PageSize), PageToken: r.PageToken})
	if err != nil {
		return nil, failure(err)
	}
	rows := make([]*onlinev1.OnlineIP, len(result.Records))
	for i, row := range result.Records {
		rows[i] = &onlinev1.OnlineIP{Ip: row.IP.Address, LastSeenUnixSeconds: row.IP.LastSeenUnixSeconds}
	}
	return &onlinev1.UserOnlineIPs{Observation: pageObservation(result), State: state(result.State), Ips: rows, Page: paging(result)}, nil
}
func metadata(o domain.Observation) *onlinev1.OnlineObservation {
	return &onlinev1.OnlineObservation{InstanceId: o.InstanceID, Email: o.Email, Generation: o.Generation, Source: domain.Source, ObservedAt: timestamppb.New(o.ObservedAt), DurationNs: int64(o.Duration)}
}
func state(s domain.State) onlinev1.OnlineState {
	switch s {
	case domain.Online:
		return onlinev1.OnlineState_ONLINE_STATE_ONLINE
	case domain.Offline:
		return onlinev1.OnlineState_ONLINE_STATE_OFFLINE
	default:
		return onlinev1.OnlineState_ONLINE_STATE_UNTRACKED
	}
}
func failure(err error) error {
	message := jsonwire.ErrorCode(err)
	code := codes.Unavailable
	switch message {
	case "invalid_argument":
		code = codes.InvalidArgument
	case "not_found":
		code = codes.NotFound
	case "facility_not_configured", "facility_unsupported", "native_identity_unsupported":
		code = codes.FailedPrecondition
	case "execution_changed", "observation_expired":
		code = codes.Aborted
	case "resource_exhausted":
		code = codes.ResourceExhausted
	case "deadline_exceeded":
		code = codes.DeadlineExceeded
	case "canceled":
		code = codes.Canceled
	}
	return status.Error(code, message)
}
