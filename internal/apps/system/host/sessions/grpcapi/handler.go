// Package grpcapi exposes native sessions and legacy login records through gRPC.
package grpcapi

import (
	"context"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/sessions/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/host/sessions/domain"
	sessionsv1 "github.com/PapilioVPN/node/internal/apps/system/host/sessions/grpcapi/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Handler struct {
	sessionsv1.UnimplementedHostSessionsServiceServer
	Service services.Service
	Timeout time.Duration
}

func (handler Handler) Register(server grpc.ServiceRegistrar) {
	sessionsv1.RegisterHostSessionsServiceServer(server, handler)
}
func summaryResult(row domain.Summary) *sessionsv1.SessionSummary {
	return &sessionsv1.SessionSummary{Id: row.ID, Uid: row.UID, User: row.User, Seat: row.Seat, LeaderPid: row.LeaderPID, Class: row.Class, Terminal: row.Terminal, Idle: row.Idle, IdleSinceMonotonicUs: row.IdleSinceMonotonicUS}
}
func (handler Handler) ListSessions(ctx context.Context, input *sessionsv1.ListSessionsRequest) (*sessionsv1.ListSessionsResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, handler.Timeout)
	defer cancel()
	result, err := handler.Service.List(ctx, domain.Selection{Offset: input.GetOffset(), Limit: input.GetLimit()})
	if err != nil {
		return nil, readError(err)
	}
	rows := make([]*sessionsv1.SessionSummary, 0, len(result.Page.Sessions))
	for _, row := range result.Page.Sessions {
		rows = append(rows, summaryResult(row))
	}
	return &sessionsv1.ListSessionsResponse{Sessions: rows, Extended: result.Page.Extended, NextOffset: result.Page.NextOffset, ObservedAt: timestamppb.New(result.ObservedAt), DurationNs: int64(result.Duration)}, nil
}
func (handler Handler) GetSession(ctx context.Context, input *sessionsv1.GetSessionRequest) (*sessionsv1.GetSessionResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, handler.Timeout)
	defer cancel()
	result, err := handler.Service.Read(ctx, input.GetId())
	if err != nil {
		return nil, readError(err)
	}
	row := result.Details
	return &sessionsv1.GetSessionResponse{Session: &sessionsv1.SessionDetails{Summary: summaryResult(row.Summary), Remote: row.Remote, RemoteHost: row.RemoteHost, RemoteUser: row.RemoteUser, StartedUnixUs: row.StartedUnixUS, StartedMonotonicUs: row.StartedMonotonicUS, Type: row.Type, State: row.State, Active: row.Active}, ObservedAt: timestamppb.New(result.ObservedAt), DurationNs: int64(result.Duration)}, nil
}
func (handler Handler) ListLoginRecords(ctx context.Context, input *sessionsv1.ListLoginRecordsRequest) (*sessionsv1.ListLoginRecordsResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, handler.Timeout)
	defer cancel()
	result, err := handler.Service.ListRecords(ctx, domain.Selection{Offset: input.GetOffset(), Limit: input.GetLimit()})
	if err != nil {
		return nil, readError(err)
	}
	rows := make([]*sessionsv1.LoginRecord, 0, len(result.Page.Records))
	for _, row := range result.Page.Records {
		rows = append(rows, &sessionsv1.LoginRecord{Pid: row.PID, RecordId: row.RecordID, User: row.User, Terminal: row.Terminal, Host: row.Host, StartedUnixUs: row.StartedUnixUS, SessionId: row.SessionID})
	}
	return &sessionsv1.ListLoginRecordsResponse{Records: rows, NextOffset: result.Page.NextOffset, ObservedAt: timestamppb.New(result.ObservedAt), DurationNs: int64(result.Duration)}, nil
}
func readError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return status.FromContextError(err).Err()
	}
	code, message := codes.Unavailable, "session observation unavailable"
	switch {
	case errors.Is(err, domain.ErrInvalidArgument):
		code, message = codes.InvalidArgument, "invalid session selection"
	case errors.Is(err, domain.ErrNotFound):
		code, message = codes.NotFound, "session not found"
	case errors.Is(err, domain.ErrPermissionDenied):
		code, message = codes.PermissionDenied, "session source access denied"
	case errors.Is(err, domain.ErrUnsupported):
		code, message = codes.Unimplemented, "session source unsupported"
	case errors.Is(err, domain.ErrResourceExhausted):
		code, message = codes.ResourceExhausted, "session inventory exceeds supported bound"
	}
	return status.Error(code, message)
}
