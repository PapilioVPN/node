package grpcapi

import (
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	instancesv1 "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/grpcapi/proto/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func timestamp(value *time.Time) *timestamppb.Timestamp {
	if value == nil {
		return nil
	}
	return timestamppb.New(*value)
}
func statusResult(s domain.Status) *instancesv1.InstanceStatus {
	e := s.Execution
	return &instancesv1.InstanceStatus{InstanceId: s.ID, Engine: s.Engine, State: string(e.State), Generation: e.Generation, StartedAt: timestamp(e.StartedAt), ExitedAt: timestamp(e.ExitedAt), ExitCode: e.ExitCode, Forced: e.Forced, ExecutionFailed: e.Failure != nil, ObservedAt: timestamppb.New(s.ObservedAt), ConfigurationRevision: e.ConfigurationRevision, ConfigurationCleanupFailed: e.CleanupFailure != nil}
}
func controlResult(result domain.Control) *instancesv1.ControlResult {
	response := &instancesv1.ControlResult{Effect: string(result.Effect)}
	if result.Status != nil {
		response.Status = statusResult(*result.Status)
	}
	return response
}
func readinessResult(result domain.Readiness) *instancesv1.InstanceReadiness {
	return &instancesv1.InstanceReadiness{Status: statusResult(result.Status), ApiState: string(result.APIState), TrafficState: string(result.TrafficState), CheckedAt: timestamppb.New(result.CheckedAt)}
}
