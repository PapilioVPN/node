package grpcapi

import (
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	configsv1 "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/grpcapi/proto/v1"
	instancesv1 "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/grpcapi/proto/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func metadataResult(m domain.DesiredMetadata) *configsv1.ConfigurationMetadata {
	result := &configsv1.ConfigurationMetadata{Revision: m.Revision, Sha256: m.SHA256, SourceCount: uint32(m.SourceCount), Durable: m.Durable}
	if !m.UpdatedAt.IsZero() {
		result.UpdatedAt = timestamppb.New(m.UpdatedAt)
	}
	return result
}
func configurationChange(id string, c domain.Change) *configsv1.ConfigurationChange {
	p := c.Persistence
	result := &configsv1.ConfigurationChange{InstanceId: id, Persistence: &configsv1.ConfigurationPersistence{Metadata: metadataResult(p.Metadata), Published: p.Published, PublicationUnknown: p.PublicationUnknown, Durable: p.Durable}}
	if control := c.Execution; control != nil {
		result.Execution = &instancesv1.ControlResult{Effect: string(control.Effect)}
		if s := control.Status; s != nil {
			e := s.Execution
			result.Execution.Status = &instancesv1.InstanceStatus{InstanceId: s.ID, Engine: s.Engine, State: string(e.State), Generation: e.Generation, StartedAt: optionalTime(e.StartedAt), ExitedAt: optionalTime(e.ExitedAt), ExitCode: e.ExitCode, Forced: e.Forced, ExecutionFailed: e.Failure != nil, ObservedAt: timestamppb.New(s.ObservedAt), ConfigurationRevision: e.ConfigurationRevision, ConfigurationCleanupFailed: e.CleanupFailure != nil}
		}
	}
	return result
}
func optionalTime(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return timestamppb.New(*t)
}
