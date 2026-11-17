// Package grpcapi adapts native user presence and IP observations to gRPC.
package grpcapi

import (
	"context"
	"errors"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/xray/online/app/validators"
	"github.com/PapilioVPN/node/internal/modules/xray/online/domain"
	onlinev1 "github.com/PapilioVPN/node/internal/modules/xray/online/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/shared/grpcidentity"
	"github.com/PapilioVPN/node/internal/shared/grpcresponse"
)

// OnlineAPI owns online observations and their independent read grant.
type OnlineAPI struct {
	Online          OnlineReader
	AllowOnlineRead bool
}

type OnlineReader interface {
	GetUserOnlineStats(context.Context, instances.InstanceID, domain.UserEmail) (domain.InstanceUserOnline, error)
	ListUserOnlineIPs(context.Context, instances.InstanceID, domain.UserEmail) (domain.InstanceUserOnlineIPs, error)
	ListOnlineUsers(context.Context, instances.InstanceID) (domain.InstanceOnlineUsers, error)
}

func (h OnlineAPI) authorizeOnline(ctx context.Context) error {
	if err := grpcidentity.VerifyClient(ctx); err != nil {
		return err
	}
	if !h.AllowOnlineRead {
		return status.Error(codes.PermissionDenied, "engine online read is not granted")
	}
	return nil
}

func (h OnlineAPI) GetUserOnlineStats(ctx context.Context, request *onlinev1.GetUserOnlineStatsRequest) (*onlinev1.GetUserOnlineStatsResponse, error) {
	if err := h.authorizeOnline(ctx); err != nil {
		return nil, err
	}
	if strings.TrimSpace(request.GetInstanceId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "instance_id is required")
	}
	email := domain.UserEmail(request.GetEmail())
	if err := validators.UserEmail(email); err != nil {
		return nil, status.Error(codes.InvalidArgument, "email is required")
	}
	result, err := h.Online.GetUserOnlineStats(ctx, instances.InstanceID(request.GetInstanceId()), email)
	if mapped := onlineReadError(ctx, err); mapped != nil {
		return nil, mapped
	}
	return &onlinev1.GetUserOnlineStatsResponse{InstanceId: string(result.InstanceID), Email: string(result.Email), OnlineIpCount: result.Online.OnlineIPCount, ObservedAtUnixMs: result.Online.ObservedAt.UnixMilli()}, nil
}

func (h OnlineAPI) ListUserOnlineIPs(ctx context.Context, request *onlinev1.ListUserOnlineIPsRequest) (*onlinev1.ListUserOnlineIPsResponse, error) {
	if err := h.authorizeOnline(ctx); err != nil {
		return nil, err
	}
	if strings.TrimSpace(request.GetInstanceId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "instance_id is required")
	}
	email := domain.UserEmail(request.GetEmail())
	if err := validators.UserEmail(email); err != nil {
		return nil, status.Error(codes.InvalidArgument, "email is required")
	}
	result, err := h.Online.ListUserOnlineIPs(ctx, instances.InstanceID(request.GetInstanceId()), email)
	if mapped := onlineReadError(ctx, err); mapped != nil {
		return nil, mapped
	}
	items := make([]*onlinev1.OnlineIP, len(result.IPs.Items))
	for i, item := range result.IPs.Items {
		items[i] = &onlinev1.OnlineIP{Ip: item.IP, LastSeenUnixSeconds: item.LastSeenUnixSeconds}
	}
	if mapped := grpcresponse.ContextError(ctx.Err()); mapped != nil {
		return nil, mapped
	}
	return &onlinev1.ListUserOnlineIPsResponse{InstanceId: string(result.InstanceID), Email: string(result.Email), Items: items, ObservedAtUnixMs: result.IPs.ObservedAt.UnixMilli()}, nil
}

func (h OnlineAPI) ListOnlineUsers(ctx context.Context, request *onlinev1.ListOnlineUsersRequest) (*onlinev1.ListOnlineUsersResponse, error) {
	if err := h.authorizeOnline(ctx); err != nil {
		return nil, err
	}
	if strings.TrimSpace(request.GetInstanceId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "instance_id is required")
	}
	result, err := h.Online.ListOnlineUsers(ctx, instances.InstanceID(request.GetInstanceId()))
	if mapped := onlineReadError(ctx, err); mapped != nil {
		return nil, mapped
	}
	emails := make([]string, len(result.Users.Emails))
	for i, email := range result.Users.Emails {
		emails[i] = string(email)
	}
	if mapped := grpcresponse.ContextError(ctx.Err()); mapped != nil {
		return nil, mapped
	}
	return &onlinev1.ListOnlineUsersResponse{InstanceId: string(result.InstanceID), Emails: emails, ObservedAtUnixMs: result.Users.ObservedAt.UnixMilli()}, nil
}

func onlineReadError(ctx context.Context, err error) error {
	if mapped := grpcresponse.ContextError(errors.Join(ctx.Err(), err)); mapped != nil {
		return mapped
	}
	switch {
	case errors.Is(err, instances.ErrNotFound):
		return status.Error(codes.NotFound, "Xray instance not found")
	case errors.Is(err, domain.ErrOnlineObservationNotFound):
		return status.Error(codes.NotFound, "Xray online observation not found")
	case errors.Is(err, domain.ErrAPIDisabled):
		return status.Error(codes.FailedPrecondition, "Xray API is not enabled")
	case errors.Is(err, domain.ErrOnlineUnsupported):
		return status.Error(codes.FailedPrecondition, "Xray online observations are not supported")
	case errors.Is(err, domain.ErrAPIBusy), errors.Is(err, domain.ErrAPIResourceLimit):
		return status.Error(codes.ResourceExhausted, "Xray API resource limit exceeded")
	case err != nil:
		return status.Error(codes.Unavailable, "cannot read Xray online observation")
	default:
		return nil
	}
}
