// Package grpcapi adapts typed controller requests to bounded log pulls.
package grpcapi

import (
	"context"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/logs/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/logs/domain"
	v1 "github.com/PapilioVPN/node/internal/apps/xray/observability/logs/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/logs/infra/jsonwire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Handler struct {
	v1.UnimplementedLogsServiceServer
	Service *services.Service
	Timeout time.Duration
}

func (h Handler) Register(r grpc.ServiceRegistrar) { v1.RegisterLogsServiceServer(r, h) }
func (h Handler) TailLogs(ctx context.Context, r *v1.TailLogsRequest) (*v1.LogPage, error) {
	if r == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	p, err := h.Service.Tail(ctx, r.InstanceId, domain.TailRequest{Limit: r.Limit})
	if err != nil {
		return nil, failure(err)
	}
	return present(p), nil
}
func (h Handler) FollowLogs(ctx context.Context, r *v1.FollowLogsRequest) (*v1.LogPage, error) {
	if r == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	p, err := h.Service.Follow(ctx, r.InstanceId, domain.FollowRequest{Cursor: r.Cursor, Limit: r.Limit, WaitMilliseconds: r.WaitMs})
	if err != nil {
		return nil, failure(err)
	}
	return present(p), nil
}
func present(p domain.Page) *v1.LogPage {
	r := &v1.LogPage{InstanceId: p.InstanceID, Source: p.Source, ExecutionId: p.ExecutionID, Generation: p.Generation, ObservedAt: timestamppb.New(p.ObservedAt), NextCursor: p.NextCursor, CapacityReached: p.CapacityReached, PositionUncertain: p.PositionUncertain, Reattached: p.Reattached, Exited: p.Exited}
	for _, v := range p.Records {
		r.Records = append(r.Records, &v1.LogRecord{StartOffset: v.StartOffset, NextOffset: v.NextOffset, Level: v.Level, Kind: v.Kind, Message: v.Message, Redacted: v.Redacted, Truncated: v.Truncated, Partial: v.Partial})
	}
	for _, v := range p.Gaps {
		r.Gaps = append(r.Gaps, &v1.LogGap{Reason: v.Reason, FromOffset: v.FromOffset, ToOffset: v.ToOffset, ExtentKnown: v.ExtentKnown, PreviousExecutionId: v.PreviousExecutionID})
	}
	return r
}
func failure(err error) error {
	code := jsonwire.ErrorCode(err)
	native := codes.Unavailable
	switch code {
	case "invalid_argument":
		native = codes.InvalidArgument
	case "not_found":
		native = codes.NotFound
	case "resource_exhausted":
		native = codes.ResourceExhausted
	case "output_unavailable", "execution_changed", "closed":
		native = codes.FailedPrecondition
	case "canceled":
		native = codes.Canceled
	case "deadline_exceeded":
		native = codes.DeadlineExceeded
	}
	return status.Error(native, code)
}
