// Package api reads the pinned ObservatoryService through an instance-owned channel.
package api

import (
	"context"
	"errors"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/outboundhealth/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/outboundhealth/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/rpc"
	command "github.com/xtls/xray-core/app/observatory/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type Reader struct {
	client command.ObservatoryServiceClient
}

// New constructs one stub, borrowing the instance's lazy, reconnecting channel.
func New(connection grpc.ClientConnInterface) Reader {
	return Reader{client: command.NewObservatoryServiceClient(connection)}
}

// Read performs one complete native collection read, with no probe or retry.
// The byte ceiling precedes real gRPC decoding; record limits follow decoding.
func (r Reader) Read(ctx context.Context) ([]domain.Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result, err := r.client.GetOutboundStatus(ctx, &command.GetOutboundStatusRequest{}, grpc.MaxCallRecvMsgSize(domain.MaxNativeBytes))
	if err != nil {
		return nil, nativeError(ctx, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if result == nil || result.Status == nil || len(result.ProtoReflect().GetUnknown()) > 0 || len(result.Status.ProtoReflect().GetUnknown()) > 0 {
		return nil, domain.ErrUnavailable
	}
	if proto.Size(result) > domain.MaxNativeBytes || len(result.Status.Status) > domain.MaxRecords {
		return nil, domain.ErrCapacity
	}
	records := make([]domain.Record, 0, len(result.Status.Status))
	for _, v := range result.Status.Status {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if v == nil || validators.Tag(v.OutboundTag) != nil || len(v.ProtoReflect().GetUnknown()) > 0 {
			return nil, domain.ErrUnavailable
		}
		record := domain.Record{Tag: v.OutboundTag, Alive: v.Alive, DelayMS: v.Delay, LastSeenUnixSec: v.LastSeenTime, LastTryUnixSec: v.LastTryTime}
		if h := v.HealthPing; h != nil {
			if len(h.ProtoReflect().GetUnknown()) > 0 {
				return nil, domain.ErrUnavailable
			}
			record.HealthPing = &domain.HealthPing{All: h.All, Fail: h.Fail, DeviationNS: h.Deviation, AverageNS: h.Average, MaxNS: h.Max, MinNS: h.Min}
		}
		records = append(records, record)
	}
	return records, nil
}
func nativeError(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, rpc.ErrClosed):
		err = errors.Join(domain.ErrClosed, err)
	case status.Code(err) == codes.Unimplemented:
		err = errors.Join(domain.ErrUnsupported, err)
	case status.Code(err) == codes.ResourceExhausted:
		err = errors.Join(domain.ErrCapacity, err)
	case status.Code(err) == codes.Canceled:
		err = errors.Join(context.Canceled, err)
	case status.Code(err) == codes.DeadlineExceeded:
		err = errors.Join(context.DeadlineExceeded, err)
	default:
		err = errors.Join(domain.ErrUnavailable, err)
	}
	return errors.Join(err, ctx.Err())
}
