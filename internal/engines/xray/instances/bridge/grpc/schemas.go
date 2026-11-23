package grpc

//go:generate protoc -I . --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative proto/v1/instances.proto

import (
	"context"
	"errors"

	instancesv1 "github.com/PapilioVPN/node/internal/engines/xray/instances/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/xray/instances/lifecycle"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func response(result lifecycle.Status, err error) (*instancesv1.XrayInstanceStatus, error) {
	var observed *instancesv1.XrayInstanceStatus
	if result.ID != "" {
		observed = encodeStatus(result)
	}
	if err == nil {
		return observed, nil
	}
	var failure *lifecycle.StartError
	if errors.As(err, &failure) {
		return nil, startFailureStatus(failure, observed)
	}
	base := publicStatus(err)
	if observed != nil {
		withDetails, detailErr := base.WithDetails(observed)
		if detailErr == nil {
			return nil, withDetails.Err()
		}
	}
	return nil, base.Err()
}

func publicStatus(err error) *status.Status {
	switch {
	case errors.Is(err, lifecycle.ErrNotFound):
		return status.New(codes.NotFound, lifecycle.ErrNotFound.Error())
	case errors.Is(err, lifecycle.ErrInvalidStart):
		return status.New(codes.InvalidArgument, lifecycle.ErrInvalidStart.Error())
	case errors.Is(err, lifecycle.ErrBusy):
		return status.New(codes.Aborted, lifecycle.ErrBusy.Error())
	case errors.Is(err, lifecycle.ErrUnavailable):
		return status.New(codes.Unavailable, lifecycle.ErrUnavailable.Error())
	case errors.Is(err, context.Canceled):
		return status.New(codes.Canceled, "xray operation canceled")
	case errors.Is(err, context.DeadlineExceeded):
		return status.New(codes.DeadlineExceeded, "xray operation deadline exceeded")
	case errors.Is(err, lifecycle.ErrStopFailed):
		return status.New(codes.Unavailable, lifecycle.ErrStopFailed.Error())
	default:
		return status.New(codes.Unavailable, "xray instance unavailable")
	}
}

func encodeStatus(result lifecycle.Status) *instancesv1.XrayInstanceStatus {
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
	var action instancesv1.XrayStartAction
	switch result.StartAction {
	case lifecycle.Started:
		action = instancesv1.XrayStartAction_XRAY_START_ACTION_STARTED
	case lifecycle.Heartbeat:
		action = instancesv1.XrayStartAction_XRAY_START_ACTION_HEARTBEAT
	case lifecycle.Restarted:
		action = instancesv1.XrayStartAction_XRAY_START_ACTION_RESTARTED
	case lifecycle.Recovered:
		action = instancesv1.XrayStartAction_XRAY_START_ACTION_RECOVERED
	}
	observed := &instancesv1.XrayInstanceStatus{Id: result.ID, State: state, XrayVersion: result.XrayVersion,
		NodeVersion: result.NodeVersion, StartAction: action}
	if !result.LeaseExpiresAt.IsZero() {
		observed.KeepAliveExpiresAt = timestamppb.New(result.LeaseExpiresAt)
	}
	return observed
}

func startFailureStatus(failure *lifecycle.StartError, observed *instancesv1.XrayInstanceStatus) error {
	var reason instancesv1.XrayStartFailureReason
	code := codes.Unavailable
	switch failure.Reason {
	case lifecycle.LaunchFailed:
		reason = instancesv1.XrayStartFailureReason_XRAY_START_FAILURE_REASON_LAUNCH_FAILED
	case lifecycle.ExitedBeforeReady:
		reason = instancesv1.XrayStartFailureReason_XRAY_START_FAILURE_REASON_EXITED_BEFORE_READY
	case lifecycle.ReadinessTimeout:
		reason = instancesv1.XrayStartFailureReason_XRAY_START_FAILURE_REASON_READINESS_TIMEOUT
	case lifecycle.ReadinessFailed:
		reason = instancesv1.XrayStartFailureReason_XRAY_START_FAILURE_REASON_READINESS_FAILED
	case lifecycle.ConfigRejected:
		reason = instancesv1.XrayStartFailureReason_XRAY_START_FAILURE_REASON_CONFIG_REJECTED
		code = codes.InvalidArgument
	case lifecycle.ConfigCheckFailed:
		reason = instancesv1.XrayStartFailureReason_XRAY_START_FAILURE_REASON_CONFIG_CHECK_FAILED
	case lifecycle.InitialAccountsFailed:
		reason = instancesv1.XrayStartFailureReason_XRAY_START_FAILURE_REASON_INITIAL_ACCOUNTS_FAILED
	}
	base := status.New(code, failure.Error())
	withDetails, err := base.WithDetails(&instancesv1.XrayStartFailure{
		Reason: reason, ExitCode: failure.ExitCode, Status: observed,
	})
	if err != nil {
		return base.Err()
	}
	return withDetails.Err()
}
