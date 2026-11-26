// Package grpc exposes Hysteria statistics and disconnect requests.
package grpc

//go:generate protoc -I . --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative proto/v1/hysteria_stats.proto

import (
	"context"

	"github.com/PapilioVPN/node/internal/engines/hysteria/internal/grpcapi"
	v1 "github.com/PapilioVPN/node/internal/engines/hysteria/stats/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/hysteria/stats/observations"
	"google.golang.org/grpc"
)

type handler struct {
	v1.UnimplementedHysteriaStatsServiceServer
	service *observations.Service
}

func Register(registrar grpc.ServiceRegistrar, service *observations.Service) {
	v1.RegisterHysteriaStatsServiceServer(registrar, &handler{service: service})
}

func (h *handler) GetRuntimeStats(ctx context.Context, r *v1.HysteriaStatsRequest) (*v1.HysteriaRuntimeStats, error) {
	v, err := h.service.GetRuntimeStats(ctx, r.GetId())
	if err != nil {
		return nil, grpcapi.ErrorStatus(err).Err()
	}
	return &v1.HysteriaRuntimeStats{Execution: v.Execution, ReadySinceUnixNano: v.ReadySinceUnixNano, UptimeSeconds: v.UptimeSeconds}, nil
}
func (h *handler) GetTraffic(ctx context.Context, r *v1.HysteriaTrafficRequest) (*v1.HysteriaTrafficEntry, error) {
	v, err := h.service.GetTraffic(ctx, r.GetId(), r.GetUserId())
	if err != nil {
		return nil, grpcapi.ErrorStatus(err).Err()
	}
	return entry(v), nil
}
func (h *handler) ListTraffic(ctx context.Context, r *v1.HysteriaStatsRequest) (*v1.HysteriaTrafficSnapshot, error) {
	rows, err := h.service.ListTraffic(ctx, r.GetId())
	return snapshot(rows, "", err)
}
func (h *handler) ResetTraffic(ctx context.Context, r *v1.HysteriaStatsRequest) (*v1.HysteriaTrafficSnapshot, error) {
	rows, err := h.service.ResetTraffic(ctx, r.GetId())
	return snapshot(rows, observations.ResetScope, err)
}
func (h *handler) ListOnlineUsers(ctx context.Context, r *v1.HysteriaStatsRequest) (*v1.HysteriaOnlineUsers, error) {
	rows, err := h.service.ListOnlineUsers(ctx, r.GetId())
	if err != nil {
		return nil, grpcapi.ErrorStatus(err).Err()
	}
	res := &v1.HysteriaOnlineUsers{Users: make([]*v1.HysteriaOnlineDeviceCount, 0, len(rows))}
	for _, row := range rows {
		res.Users = append(res.Users, &v1.HysteriaOnlineDeviceCount{UserId: row.UserID, Devices: row.Devices})
	}
	return res, nil
}
func (h *handler) DisconnectUsers(ctx context.Context, r *v1.DisconnectHysteriaUsersRequest) (*v1.HysteriaDisconnectResult, error) {
	if err := h.service.DisconnectUsers(ctx, r.GetId(), r.GetUserIds()); err != nil {
		return nil, grpcapi.ErrorStatus(err).Err()
	}
	return &v1.HysteriaDisconnectResult{Acknowledged: true}, nil
}

func entry(v observations.Entry) *v1.HysteriaTrafficEntry {
	return &v1.HysteriaTrafficEntry{UserId: v.UserID, TxBytes: v.TX, RxBytes: v.RX}
}

func snapshot(rows []observations.Entry, scope string, err error) (*v1.HysteriaTrafficSnapshot, error) {
	if err != nil {
		return nil, grpcapi.ErrorStatus(err).Err()
	}
	res := &v1.HysteriaTrafficSnapshot{Entries: make([]*v1.HysteriaTrafficEntry, 0, len(rows)), ResetScope: scope}
	for _, v := range rows {
		res.Entries = append(res.Entries, entry(v))
	}
	return res, nil
}
