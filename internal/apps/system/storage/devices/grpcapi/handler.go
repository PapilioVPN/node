// Package grpcapi adapts device I/O observations to versioned gRPC.
package grpcapi

import (
	"context"
	"errors"
	"github.com/PapilioVPN/node/internal/apps/system/storage/devices/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/storage/devices/domain"
	devicesv1 "github.com/PapilioVPN/node/internal/apps/system/storage/devices/grpcapi/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	"time"
)

type Handler struct {
	devicesv1.UnimplementedDeviceIOServiceServer
	Service services.Service
	Timeout time.Duration
}

func (handler Handler) Register(server grpc.ServiceRegistrar) {
	devicesv1.RegisterDeviceIOServiceServer(server, handler)
}
func (handler Handler) ListDevices(ctx context.Context, input *devicesv1.ListDevicesRequest) (*devicesv1.ListDevicesResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, handler.Timeout)
	defer cancel()
	result, err := handler.Service.List(ctx, domain.Selection{AfterName: input.GetAfterName(), Limit: input.GetLimit()})
	if err != nil {
		return nil, readError(err)
	}
	rows := make([]*devicesv1.Device, 0, len(result.Page.Devices))
	for _, row := range result.Page.Devices {
		value := &devicesv1.Device{Major: row.Major, Minor: row.Minor, Name: row.Name, ReadsCompleted: row.ReadsCompleted, ReadsMerged: row.ReadsMerged, ReadSectors: row.ReadSectors, ReadMilliseconds: row.ReadMilliseconds, WritesCompleted: row.WritesCompleted, WritesMerged: row.WritesMerged, WriteSectors: row.WriteSectors, WriteMilliseconds: row.WriteMilliseconds, InProgress: row.InProgress, ActiveMilliseconds: row.ActiveMilliseconds, WeightedMilliseconds: row.WeightedMilliseconds}
		if row.Discard != nil {
			value.Discard = &devicesv1.Discard{Completed: row.Discard.Completed, Merged: row.Discard.Merged, Sectors: row.Discard.Sectors, Milliseconds: row.Discard.Milliseconds}
		}
		if row.Flush != nil {
			value.Flush = &devicesv1.Flush{Completed: row.Flush.Completed, Milliseconds: row.Flush.Milliseconds}
		}
		rows = append(rows, value)
	}
	return &devicesv1.ListDevicesResponse{Devices: rows, NextAfterName: result.Page.NextAfterName, SectorSizeBytes: 512, ObservedAt: timestamppb.New(result.ObservedAt), DurationNs: int64(result.Duration)}, nil
}
func readError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return status.FromContextError(err).Err()
	}
	switch {
	case errors.Is(err, domain.ErrInvalidArgument):
		return status.Error(codes.InvalidArgument, "invalid device selection")
	case errors.Is(err, domain.ErrResourceExhausted):
		return status.Error(codes.ResourceExhausted, "device inventory exceeds supported bound")
	}
	return status.Error(codes.Unavailable, "device I/O unavailable")
}
