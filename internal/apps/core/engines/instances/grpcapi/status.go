package grpcapi

import (
	"errors"
	"math"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/apps/core/engines/instances/domain"
	instancesv1 "github.com/PapilioVPN/node/internal/apps/core/engines/instances/grpcapi/proto/v1"
)

func statusResponse(value domain.Status) (*instancesv1.GetInstanceStatusResponse, error) {
	state, err := stateCode(value.State)
	if err != nil {
		return nil, err
	}
	return &instancesv1.GetInstanceStatusResponse{
		InstanceId: string(value.Instance.ID), Engine: string(value.Instance.Kind), State: state, Failure: executionFailure(value.Failure),
	}, nil
}

func instanceStatus(value domain.Status) (*instancesv1.InstanceStatus, error) {
	state, err := stateCode(value.State)
	if err != nil {
		return nil, err
	}
	return &instancesv1.InstanceStatus{
		InstanceId: string(value.Instance.ID), Engine: string(value.Instance.Kind),
		State: state, Failure: executionFailure(value.Failure),
	}, nil
}

func stateCode(value domain.State) (instancesv1.InstanceState, error) {
	switch value {
	case domain.Stopped:
		return instancesv1.InstanceState_INSTANCE_STATE_STOPPED, nil
	case domain.Running:
		return instancesv1.InstanceState_INSTANCE_STATE_RUNNING, nil
	case domain.Stopping:
		return instancesv1.InstanceState_INSTANCE_STATE_STOPPING, nil
	case domain.Exited:
		return instancesv1.InstanceState_INSTANCE_STATE_EXITED, nil
	default:
		return instancesv1.InstanceState_INSTANCE_STATE_UNSPECIFIED, status.Error(codes.Internal, "cannot represent instance state")
	}
}

func executionFailure(cause error) *instancesv1.InstanceFailure {
	if cause == nil {
		return nil
	}
	failure := &instancesv1.InstanceFailure{Code: instancesv1.InstanceFailureCode_INSTANCE_FAILURE_CODE_EXECUTION_FAILED}
	// Native exec.ExitError exposes ExitCode through its embedded ProcessState.
	// Only this error capability crosses the boundary, never raw native text.
	var exit interface{ ExitCode() int }
	if errors.As(cause, &exit) {
		if code := exit.ExitCode(); code >= 0 && code <= math.MaxInt32 {
			code := int32(code)
			failure.ExitCode = &code
		}
	}
	return failure
}
