package grpcapi

import (
	"context"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/domain"
	v1 "github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/grpcapi/proto/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func summary(s domain.Summary) *v1.InboundSummary {
	return &v1.InboundSummary{Tag: s.Tag, Direction: s.Direction, Protocol: s.Protocol, NativeType: s.NativeType, AccountType: s.AccountType, LiveAccounts: s.LiveAccounts, Reason: s.Reason, EmailMatching: s.EmailMatching}
}
func (h Handler) ListInboundSummaries(ctx context.Context, r *v1.ListInboundSummariesRequest) (*v1.InboundSummaries, error) {
	if r == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Catalog.List(ctx, r.InstanceId, domain.PageRequest{Size: r.PageSize, Cursor: r.Cursor})
	if err != nil {
		return nil, failure(err)
	}
	items := make([]*v1.InboundSummary, len(result.Inbounds))
	for i, s := range result.Inbounds {
		items[i] = summary(s)
	}
	return &v1.InboundSummaries{InstanceId: result.InstanceID, Generation: result.Generation, Inbounds: items, ObservedAt: timestamppb.New(result.ObservedAt), DurationNs: int64(result.Duration), NextCursor: result.NextCursor, ExpiresAt: timestamppb.New(result.ExpiresAt)}, nil
}
func (h Handler) GetInboundCompatibility(ctx context.Context, r *v1.GetInboundCompatibilityRequest) (*v1.InboundCompatibility, error) {
	if r == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Catalog.Compatibility(ctx, r.InstanceId, r.Tag)
	if err != nil {
		return nil, failure(err)
	}
	return &v1.InboundCompatibility{InstanceId: result.InstanceID, Generation: result.Generation, Inbound: summary(result.Inbound), ObservedAt: timestamppb.New(result.ObservedAt), DurationNs: int64(result.Duration)}, nil
}
func (h Handler) PlanInboundReplacement(ctx context.Context, r *v1.PlanInboundReplacementRequest) (*v1.InboundReplacementPlan, error) {
	if r == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Catalog.Plan(ctx, r.InstanceId, domain.ReplacementInput{Tag: r.Tag, Configuration: r.Configuration, AllowInterruption: r.AllowInterruption, DisconnectExisting: r.DisconnectExisting})
	if err != nil {
		return nil, failure(err)
	}
	return &v1.InboundReplacementPlan{Token: result.Token, InstanceId: result.InstanceID, Generation: result.Generation, Tag: result.Tag, CurrentSha256: result.CurrentSHA256, CandidateSha256: result.CandidateSHA256, Mode: result.Mode, Reason: result.Reason, ExistingConnections: result.ExistingConnections, NewConnections: result.NewConnections, Fallback: result.Fallback, ExpiresAt: timestamppb.New(result.ExpiresAt)}, nil
}
func (h Handler) ReplaceInbound(ctx context.Context, r *v1.ReplaceInboundRequest) (*v1.InboundOperation, error) {
	if r == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Catalog.Replace(ctx, r.InstanceId, r.Token)
	return mutationResult(result, err)
}
