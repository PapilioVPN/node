package grpc

import (
	"context"

	pb "github.com/PapilioVPN/node/internal/engines/openvpn/instances/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/openvpn/instances/lifecycle"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

func (h *handlers) ListSessions(ctx context.Context, req *pb.OpenVPNInstanceRequest) (*pb.OpenVPNSessionReport, error) {
	report, err := h.service.Sessions(ctx, req.GetId())
	if err != nil {
		return nil, rpcError(err).Err()
	}
	r := &pb.OpenVPNSessionReport{
		ExecutionId: report.ExecutionID, ObservedAt: report.ObservedAt,
		Sessions: make([]*pb.OpenVPNSession, 0, len(report.Sessions)),
	}
	for _, s := range report.Sessions {
		r.Sessions = append(r.Sessions, &pb.OpenVPNSession{
			Id: s.ID, CommonName: s.CommonName, RemoteAddress: s.RemoteAddress,
			TunnelIpv4: s.TunnelIPv4, TunnelIpv6: s.TunnelIPv6, RxBytes: s.RXBytes, TxBytes: s.TXBytes,
			ConnectedAt: s.ConnectedAt, Cipher: s.Cipher,
		})
	}
	return r, nil
}

func (h *handlers) DisconnectSession(ctx context.Context, req *pb.DisconnectOpenVPNSessionRequest) (*emptypb.Empty, error) {
	if req.SessionId == nil || req.GetExecutionId() == "" {
		return nil, status.Error(codes.InvalidArgument, "execution_id and session_id are required")
	}
	if err := h.service.Disconnect(ctx, req.GetId(), req.GetExecutionId(), req.GetSessionId()); err != nil {
		return nil, rpcError(err).Err()
	}
	return &emptypb.Empty{}, nil
}

func (h *handlers) GetLogs(req *pb.OpenVPNLogsRequest, stream pb.OpenVPNOperationsService_GetLogsServer) error {
	err := h.service.FollowLogs(stream.Context(), req.GetId(), req.GetExecutionId(), req.GetAfterSequence(), func(batch lifecycle.LogBatch) error {
		r := &pb.OpenVPNLogBatch{ExecutionId: batch.ExecutionID, Dropped: batch.Dropped, Entries: make([]*pb.OpenVPNLog, 0, len(batch.Entries))}
		for _, entry := range batch.Entries {
			r.Entries = append(r.Entries, &pb.OpenVPNLog{
				Sequence: entry.Sequence, Timestamp: entry.Timestamp, Level: entry.Level, Message: entry.Message,
			})
		}
		return stream.Send(r)
	})
	if err != nil {
		return rpcError(err).Err()
	}
	return nil
}
