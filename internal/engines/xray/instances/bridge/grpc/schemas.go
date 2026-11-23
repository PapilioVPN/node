package grpc

//go:generate protoc -I . --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative proto/v1/instances.proto

import (
	"context"
	"errors"

	instancesv1 "github.com/PapilioVPN/node/internal/engines/xray/instances/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/xray/instances/lifecycle"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func response(result lifecycle.Status, err error) (*instancesv1.XrayInstanceStatus, error) {
	if err != nil {
		var failure *lifecycle.StartError
		switch {
		case errors.As(err, &failure):
			return nil, startFailureStatus(failure)
		case errors.Is(err, lifecycle.ErrNotFound):
			return nil, status.Error(codes.NotFound, lifecycle.ErrNotFound.Error())
		case errors.Is(err, lifecycle.ErrBusy):
			return nil, status.Error(codes.Aborted, lifecycle.ErrBusy.Error())
		case errors.Is(err, context.Canceled):
			return nil, status.Error(codes.Canceled, "xray operation canceled")
		case errors.Is(err, context.DeadlineExceeded):
			return nil, status.Error(codes.DeadlineExceeded, "xray operation deadline exceeded")
		case errors.Is(err, lifecycle.ErrStopFailed):
			return nil, status.Error(codes.Unavailable, lifecycle.ErrStopFailed.Error())
		default:
			return nil, status.Error(codes.Unavailable, "xray instance unavailable")
		}
	}

	var state instancesv1.XrayInstanceState
	switch result.State {
	case lifecycle.Stopped:
		state = instancesv1.XrayInstanceState_XRAY_INSTANCE_STATE_STOPPED
	case lifecycle.Starting:
		state = instancesv1.XrayInstanceState_XRAY_INSTANCE_STATE_STARTING
	case lifecycle.Ready:
		state = instancesv1.XrayInstanceState_XRAY_INSTANCE_STATE_READY
	case lifecycle.Stopping:
		state = instancesv1.XrayInstanceState_XRAY_INSTANCE_STATE_STOPPING
	case lifecycle.Failed:
		state = instancesv1.XrayInstanceState_XRAY_INSTANCE_STATE_FAILED
	}
	return &instancesv1.XrayInstanceStatus{Id: result.ID, State: state}, nil
}

func startFailureStatus(failure *lifecycle.StartError) error {
	var reason instancesv1.XrayStartFailureReason
	switch failure.Reason {
	case lifecycle.LaunchFailed:
		reason = instancesv1.XrayStartFailureReason_XRAY_START_FAILURE_REASON_LAUNCH_FAILED
	case lifecycle.ExitedBeforeReady:
		reason = instancesv1.XrayStartFailureReason_XRAY_START_FAILURE_REASON_EXITED_BEFORE_READY
	case lifecycle.ReadinessTimeout:
		reason = instancesv1.XrayStartFailureReason_XRAY_START_FAILURE_REASON_READINESS_TIMEOUT
	case lifecycle.ReadinessFailed:
		reason = instancesv1.XrayStartFailureReason_XRAY_START_FAILURE_REASON_READINESS_FAILED
	}
	base := status.New(codes.Unavailable, failure.Error())
	withDetails, err := base.WithDetails(&instancesv1.XrayStartFailure{
		Reason: reason, ExitCode: failure.ExitCode,
	})
	if err != nil {
		return base.Err()
	}
	return withDetails.Err()
}
