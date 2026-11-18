// Package grpcapi adapts filesystem observations to versioned gRPC.
package grpcapi

import (
	"context"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/storage/filesystems/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/storage/filesystems/domain"
	filesystemsv1 "github.com/PapilioVPN/node/internal/apps/system/storage/filesystems/grpcapi/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Handler struct {
	filesystemsv1.UnimplementedFilesystemsServiceServer
	Service services.Service
	Timeout time.Duration
}

func (handler Handler) Register(server grpc.ServiceRegistrar) {
	filesystemsv1.RegisterFilesystemsServiceServer(server, handler)
}
func (handler Handler) ListMounts(ctx context.Context, input *filesystemsv1.ListMountsRequest) (*filesystemsv1.ListMountsResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, handler.Timeout)
	defer cancel()
	result, err := handler.Service.List(ctx, domain.Selection{AfterID: input.GetAfterId(), Limit: input.GetLimit()})
	if err != nil {
		return nil, readError(err)
	}
	rows := make([]*filesystemsv1.Mount, 0, len(result.Page.Mounts))
	for _, row := range result.Page.Mounts {
		rows = append(rows, &filesystemsv1.Mount{Id: row.ID, ParentId: row.ParentID, DeviceMajor: row.DeviceMajor, DeviceMinor: row.DeviceMinor, Root: row.Root, Path: row.Path, Filesystem: row.Filesystem, Source: row.Source, ReadOnly: row.ReadOnly})
	}
	return &filesystemsv1.ListMountsResponse{Mounts: rows, NextAfterId: result.Page.NextAfterID, ObservedAt: timestamppb.New(result.ObservedAt), DurationNs: int64(result.Duration)}, nil
}
func (handler Handler) GetFilesystemUsage(ctx context.Context, input *filesystemsv1.GetFilesystemUsageRequest) (*filesystemsv1.GetFilesystemUsageResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, handler.Timeout)
	defer cancel()
	result, err := handler.Service.Usage(ctx, input.GetPath())
	if err != nil {
		return nil, readError(err)
	}
	row := result.Usage
	return &filesystemsv1.GetFilesystemUsageResponse{Usage: &filesystemsv1.FilesystemUsage{Path: row.Path, FilesystemType: row.FilesystemType, Flags: row.Flags, BlockSizeBytes: row.BlockSizeBytes, TotalBytes: row.TotalBytes, FreeBytes: row.FreeBytes, AvailableBytes: row.AvailableBytes, UsedBytes: row.UsedBytes, ReservedBytes: row.ReservedBytes, InodesTotal: row.InodesTotal, InodesFree: row.InodesFree, InodesUsed: row.InodesUsed}, ObservedAt: timestamppb.New(result.ObservedAt), DurationNs: int64(result.Duration)}, nil
}
func readError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return status.FromContextError(err).Err()
	}
	code, message := codes.Unavailable, "filesystem observation unavailable"
	switch {
	case errors.Is(err, domain.ErrInvalidArgument):
		code, message = codes.InvalidArgument, "invalid filesystem selection"
	case errors.Is(err, domain.ErrNotFound):
		code, message = codes.NotFound, "filesystem path not found"
	case errors.Is(err, domain.ErrPermissionDenied):
		code, message = codes.PermissionDenied, "filesystem access denied"
	case errors.Is(err, domain.ErrResourceExhausted):
		code, message = codes.ResourceExhausted, "mount inventory exceeds supported bound"
	}
	return status.Error(code, message)
}
