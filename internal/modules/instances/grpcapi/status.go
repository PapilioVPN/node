package grpcapi

import (
	"errors"
	"math"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	nodev1 "github.com/PapilioVPN/node/internal/entrypoints/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/modules/instances/domain"
)

func statusResponse(value domain.Status) (*nodev1.GetInstanceStatusResponse, error) {
	var state nodev1.InstanceState
	switch value.State {
	case domain.Stopped:
		state = nodev1.InstanceState_INSTANCE_STATE_STOPPED
	case domain.Running:
		state = nodev1.InstanceState_INSTANCE_STATE_RUNNING
	case domain.Stopping:
		state = nodev1.InstanceState_INSTANCE_STATE_STOPPING
	case domain.Exited:
		state = nodev1.InstanceState_INSTANCE_STATE_EXITED
	default:
		return nil, status.Error(codes.Internal, "cannot represent instance state")
	}
	response := &nodev1.GetInstanceStatusResponse{
		InstanceId: string(value.Instance.ID), Engine: string(value.Instance.Kind), State: state,
	}
	if value.Failure != nil {
		response.Failure = &nodev1.InstanceFailure{Code: nodev1.InstanceFailureCode_INSTANCE_FAILURE_CODE_EXECUTION_FAILED}
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
