package native

import (
	"context"
	"errors"

	"github.com/PapilioVPN/node/internal/engines/xray/instances/lifecycle"
	"github.com/PapilioVPN/node/internal/engines/xray/outbounds/health"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

const getOutboundStatus = "/xray.core.app.observatory.command.ObservatoryService/GetOutboundStatus"

var readOptions = []grpc.CallOption{grpc.StaticMethod(), grpc.MaxCallRecvMsgSize(1 << 20)}

type instances interface {
	NativeConnection(string) (*grpc.ClientConn, error)
}

type Observatory struct{ instances instances }

func NewObservatory(instances instances) *Observatory {
	return &Observatory{instances: instances}
}

func (client *Observatory) Read(ctx context.Context, id string) ([]health.Observation, error) {
	conn, err := client.instances.NativeConnection(id)
	if errors.Is(err, lifecycle.ErrNotFound) {
		return nil, health.ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	var response OutboundStatusResponse
	err = conn.Invoke(ctx, getOutboundStatus, &emptypb.Empty{}, &response, readOptions...)
	if cause := ctx.Err(); cause != nil {
		return nil, cause
	}
	current, stateErr := client.instances.NativeConnection(id)
	if stateErr != nil || current != conn {
		return nil, health.ErrUnavailable
	}
	switch status.Code(err) {
	case codes.OK:
	case codes.Unimplemented:
		return nil, health.ErrDisabled
	case codes.ResourceExhausted:
		return nil, health.ErrTooLarge
	default:
		return nil, err
	}
	if response.Status == nil {
		return nil, health.ErrUnavailable
	}

	records := response.Status.Status
	samples := make([]health.Observation, len(records))
	for i, record := range records {
		sample := &samples[i]
		sample.Tag = record.OutboundTag
		sample.DelayMilliseconds = &record.Delay
		sample.Status = health.Failed
		if record.Alive {
			sample.Status = health.Healthy
		}
		if record.LastSeenTime != 0 {
			sample.LastSeenUnixSeconds = &record.LastSeenTime
		}
		if record.LastTryTime != 0 {
			sample.LastTryUnixSeconds = &record.LastTryTime
		}
	}
	return samples, nil
}
