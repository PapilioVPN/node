package grpc

import (
	"context"
	"errors"

	pb "github.com/PapilioVPN/node/internal/engines/openvpn/instances/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/openvpn/instances/lifecycle"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func instanceResult(value lifecycle.Status, err error) (*pb.OpenVPNInstanceStatus, error) {
	r := &pb.OpenVPNInstanceStatus{
		Id: value.ID, State: instanceState(value.State), NodeVersion: value.NodeVersion,
		OpenvpnVersion: value.OpenVPNVersion, StartAction: startAction(value.StartAction),
	}
	if value.AppliedConfigSHA256 != "" {
		r.AppliedConfigSha256 = new(value.AppliedConfigSHA256)
	}
	if err == nil {
		return r, nil
	}
	failure := rpcError(err)
	if value.ID != "" {
		if detailed, err := failure.WithDetails(r); err == nil {
			failure = detailed
		}
	}
	return nil, failure.Err()
}

func rpcError(err error) *status.Status {
	switch {
	case errors.Is(err, lifecycle.ErrNotFound):
		return status.New(codes.NotFound, lifecycle.ErrNotFound.Error())
	case errors.Is(err, lifecycle.ErrBusy):
		return status.New(codes.Aborted, lifecycle.ErrBusy.Error())
	case errors.Is(err, lifecycle.ErrTooLarge):
		return status.New(codes.ResourceExhausted, lifecycle.ErrTooLarge.Error())
	case errors.Is(err, lifecycle.ErrRejected):
		return status.New(codes.InvalidArgument, lifecycle.ErrRejected.Error())
	case errors.Is(err, context.DeadlineExceeded):
		return status.New(codes.DeadlineExceeded, "openvpn operation deadline exceeded")
	case errors.Is(err, context.Canceled):
		return status.New(codes.Canceled, "openvpn operation canceled")
	case errors.Is(err, lifecycle.ErrSourceUnavailable):
		return status.New(codes.Unavailable, lifecycle.ErrSourceUnavailable.Error())
	case errors.Is(err, lifecycle.ErrCheckFailed):
		return status.New(codes.Unavailable, lifecycle.ErrCheckFailed.Error())
	case errors.Is(err, lifecycle.ErrStopFailed):
		return status.New(codes.Unavailable, lifecycle.ErrStopFailed.Error())
	default:
		return status.New(codes.Unavailable, lifecycle.ErrStartFailed.Error())
	}
}

func instanceState(s lifecycle.State) pb.OpenVPNInstanceState {
	switch s {
	case lifecycle.Stopped:
		return pb.OpenVPNInstanceState_OPEN_VPN_INSTANCE_STATE_STOPPED
	case lifecycle.Starting:
		return pb.OpenVPNInstanceState_OPEN_VPN_INSTANCE_STATE_STARTING
	case lifecycle.Ready:
		return pb.OpenVPNInstanceState_OPEN_VPN_INSTANCE_STATE_READY
	case lifecycle.Stopping:
		return pb.OpenVPNInstanceState_OPEN_VPN_INSTANCE_STATE_STOPPING
	case lifecycle.Failed:
		return pb.OpenVPNInstanceState_OPEN_VPN_INSTANCE_STATE_FAILED
	default:
		return pb.OpenVPNInstanceState_OPEN_VPN_INSTANCE_STATE_UNSPECIFIED
	}
}

func startAction(action lifecycle.StartAction) pb.OpenVPNStartAction {
	switch action {
	case lifecycle.Started:
		return pb.OpenVPNStartAction_OPEN_VPN_START_ACTION_STARTED
	case lifecycle.Heartbeat:
		return pb.OpenVPNStartAction_OPEN_VPN_START_ACTION_HEARTBEAT
	case lifecycle.Restarted:
		return pb.OpenVPNStartAction_OPEN_VPN_START_ACTION_RESTARTED
	case lifecycle.Recovered:
		return pb.OpenVPNStartAction_OPEN_VPN_START_ACTION_RECOVERED
	default:
		return pb.OpenVPNStartAction_OPEN_VPN_START_ACTION_UNSPECIFIED
	}
}

func issueCode(code string) pb.OpenVPNConfigIssueCode {
	switch code {
	case "invalid_source":
		return pb.OpenVPNConfigIssueCode_OPEN_VPN_CONFIG_ISSUE_CODE_INVALID_SOURCE
	case "unsupported_directive":
		return pb.OpenVPNConfigIssueCode_OPEN_VPN_CONFIG_ISSUE_CODE_UNSUPPORTED_DIRECTIVE
	case "options_rejected":
		return pb.OpenVPNConfigIssueCode_OPEN_VPN_CONFIG_ISSUE_CODE_OPTIONS_REJECTED
	default:
		return pb.OpenVPNConfigIssueCode_OPEN_VPN_CONFIG_ISSUE_CODE_UNSPECIFIED
	}
}
