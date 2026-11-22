package grpcapi

import (
	"context"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	v1 "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/grpcapi/proto/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (h Handler) PlanConfiguration(ctx context.Context, r *v1.PlanConfigurationRequest) (*v1.ConfigurationPlan, error) {
	if r == nil || r.ExpectedRevision == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	if len(r.Sources) > domain.MaxSources {
		return nil, failure(domain.ErrTooLarge)
	}
	sources := make([]domain.Source, len(r.Sources))
	for i, s := range r.Sources {
		if s == nil {
			return nil, failure(domain.ErrInvalidArgument)
		}
		sources[i] = domain.Source{Name: s.Name, Content: s.Content}
	}
	if h.Plans == nil {
		return nil, failure(domain.ErrUnsupported)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	p, err := h.Plans.Plan(ctx, r.InstanceId, *r.ExpectedRevision, sources)
	if err != nil {
		return nil, failure(err)
	}
	result := &v1.ConfigurationPlan{PlanId: p.ID, InstanceId: p.InstanceID, ExpectedRevision: p.ExpectedRevision, PreviousSha256: p.PreviousSHA256, CandidateSha256: p.CandidateSHA256, ExecutionId: p.ExecutionID, Generation: p.Generation, Mode: string(p.Mode), Reason: p.Reason, ConnectionsRetained: p.ConnectionsRetained, CreatedAt: timestamppb.New(p.CreatedAt), ExpiresAt: timestamppb.New(p.ExpiresAt)}
	for _, d := range p.Differences {
		result.Differences = append(result.Differences, &v1.ConfigurationDifference{Section: d.Section, BeforeCount: d.Before, AfterCount: d.After})
	}
	return result, nil
}
func (h Handler) ApplyConfigurationPlan(ctx context.Context, r *v1.ApplyConfigurationPlanRequest) (*v1.ConfigurationApplication, error) {
	if r == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	if h.Plans == nil {
		return nil, failure(domain.ErrUnsupported)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Plans.Apply(ctx, r.InstanceId, r.PlanId)
	if result.ID == "" && err != nil {
		return nil, failure(err)
	}
	return applicationResult(result), nil
}
func (h Handler) GetConfigurationApplication(ctx context.Context, r *v1.GetConfigurationApplicationRequest) (*v1.ConfigurationApplication, error) {
	if r == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	if h.Plans == nil {
		return nil, failure(domain.ErrUnsupported)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Plans.Application(ctx, r.InstanceId, r.ApplicationId)
	if err != nil {
		return nil, failure(err)
	}
	return applicationResult(result), nil
}
func (h Handler) RecoverConfiguration(ctx context.Context, r *v1.RecoverConfigurationRequest) (*v1.ConfigurationApplication, error) {
	if r == nil || r.ExpectedRevision == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	if h.Plans == nil {
		return nil, failure(domain.ErrUnsupported)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Plans.Recover(ctx, r.InstanceId, r.ApplicationId, *r.ExpectedRevision, r.RecoveryId)
	if result.ID == "" && err != nil {
		return nil, failure(err)
	}
	return applicationResult(result), nil
}
func persistenceResult(p domain.Persistence) *v1.ConfigurationPersistence {
	return &v1.ConfigurationPersistence{Metadata: metadataResult(p.Metadata), Published: p.Published, PublicationUnknown: p.PublicationUnknown, Durable: p.Durable}
}
func applicationResult(a domain.Application) *v1.ConfigurationApplication {
	result := &v1.ConfigurationApplication{ApplicationId: a.ID, InstanceId: a.InstanceID, Epoch: a.Epoch, ExpectedRevision: a.ExpectedRevision, Revision: a.Revision, PreviousRevision: a.PreviousRevision, CandidateSha256: a.CandidateSHA256, Mode: string(a.Mode), Outcome: a.Outcome, Phase: a.Phase, EngineEffect: a.EngineEffect, Acknowledged: a.Acknowledged, Readiness: a.Readiness, Error: a.Error, ExecutionId: a.ExecutionID, Generation: a.Generation, Persistence: persistenceResult(a.Persistence), ReceiptDurable: a.ReceiptDurable, RecoveryAvailable: a.RecoveryAvailable, RequiresRecovery: a.RequiresRecovery, CompletedAt: optionalTime(a.CompletedAt)}
	if !a.StartedAt.IsZero() {
		result.StartedAt = timestamppb.New(a.StartedAt)
	}
	if r := a.Recovery; r != nil {
		result.Recovery = &v1.ConfigurationRecovery{RecoveryId: r.ID, ExpectedRevision: r.ExpectedRevision, Revision: r.Revision, Outcome: r.Outcome, EngineEffect: r.EngineEffect, Readiness: r.Readiness, Error: r.Error, ExecutionId: r.ExecutionID, Generation: r.Generation, Persistence: persistenceResult(r.Persistence), CompletedAt: optionalTime(r.CompletedAt)}
		if !r.StartedAt.IsZero() {
			result.Recovery.StartedAt = timestamppb.New(r.StartedAt)
		}
	}
	return result
}
