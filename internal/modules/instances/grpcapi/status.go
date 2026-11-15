package grpcapi

import (
	"errors"
	"math"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/modules/instances/domain"
	instancesv1 "github.com/PapilioVPN/node/internal/modules/instances/grpcapi/proto/v1"
)

func statusResponse(value domain.Status) (*instancesv1.GetInstanceStatusResponse, error) {
	var state instancesv1.InstanceState
	switch value.State {
	case domain.Stopped:
		state = instancesv1.InstanceState_INSTANCE_STATE_STOPPED
	case domain.Running:
		state = instancesv1.InstanceState_INSTANCE_STATE_RUNNING
	case domain.Stopping:
		state = instancesv1.InstanceState_INSTANCE_STATE_STOPPING
	case domain.Exited:
		state = instancesv1.InstanceState_INSTANCE_STATE_EXITED
	default:
		return nil, status.Error(codes.Internal, "cannot represent instance state")
	}
	response := &instancesv1.GetInstanceStatusResponse{
		InstanceId: string(value.Instance.ID), Engine: string(value.Instance.Kind), State: state,
	}
	if value.Failure != nil {
		response.Failure = &instancesv1.InstanceFailure{Code: instancesv1.InstanceFailureCode_INSTANCE_FAILURE_CODE_EXECUTION_FAILED}
		// Native exec.ExitError exposes ExitCode through its embedded ProcessState.
		// The adapter needs this error capability, not a concrete Xray/OS dependency.
		var exit interface{ ExitCode() int }
		if errors.As(value.Failure, &exit) {
			if code := exit.ExitCode(); code >= 0 && code <= math.MaxInt32 {
				code := int32(code)
				response.Failure.ExitCode = &code
			}
		}
	}
	return response, nil
}
