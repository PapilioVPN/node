package grpcapi

import (
	"context"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/domain"
	v1 "github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/grpcapi/proto/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func scope(value v1.Scope) domain.Scope {
	switch value {
	case v1.Scope_SCOPE_USER:
		return domain.User
	case v1.Scope_SCOPE_INBOUND:
		return domain.Inbound
	case v1.Scope_SCOPE_OUTBOUND:
		return domain.Outbound
	}
	return ""
}
func direction(value v1.Direction) domain.Direction {
	switch value {
	case v1.Direction_DIRECTION_UPLINK:
		return domain.Uplink
	case v1.Direction_DIRECTION_DOWNLINK:
		return domain.Downlink
	}
	return ""
}
func scopeWire(value domain.Scope) v1.Scope {
	switch value {
	case domain.User:
		return v1.Scope_SCOPE_USER
	case domain.Inbound:
		return v1.Scope_SCOPE_INBOUND
	case domain.Outbound:
		return v1.Scope_SCOPE_OUTBOUND
	}
	return 0
}
func directionWire(value domain.Direction) v1.Direction {
	switch value {
	case domain.Uplink:
		return v1.Direction_DIRECTION_UPLINK
	case domain.Downlink:
		return v1.Direction_DIRECTION_DOWNLINK
	}
	return 0
}
func counterWire(r *domain.Observation) *v1.TrafficCounter {
	if r == nil {
		return nil
	}
	return &v1.TrafficCounter{InstanceId: r.InstanceID, Generation: r.Generation, Scope: scopeWire(r.Selection.Scope), Name: r.Selection.Name, Direction: directionWire(r.Selection.Direction), Bytes: r.Bytes, Source: r.Source, ObservedAt: timestamppb.New(r.ObservedAt), DurationNs: int64(r.Duration)}
}
func (h Handler) ListTrafficCounters(ctx context.Context, r *v1.ListTrafficCountersRequest) (*v1.TrafficPage, error) {
	if r == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Service.List(ctx, r.InstanceId, domain.PageRequest{Scope: scope(r.Scope), PageSize: r.PageSize, PageToken: r.PageToken})
	if err != nil {
		return nil, failure(err)
	}
	response := &v1.TrafficPage{InstanceId: result.InstanceID, Generation: result.Generation, Scope: scopeWire(result.Scope), Source: result.Source, ObservedAt: timestamppb.New(result.ObservedAt), DurationNs: int64(result.Duration), NextPageToken: result.NextPageToken}
	for _, record := range result.Counters {
		response.Counters = append(response.Counters, &v1.CounterValue{Name: record.Selection.Name, Direction: directionWire(record.Selection.Direction), Bytes: record.Bytes})
	}
	return response, nil
}
func (h Handler) GetTrafficPair(ctx context.Context, r *v1.GetTrafficPairRequest) (*v1.TrafficPair, error) {
	if r == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Service.ReadPair(ctx, r.InstanceId, scope(r.Scope), r.Name)
	if err != nil {
		return nil, failure(err)
	}
	return &v1.TrafficPair{InstanceId: result.InstanceID, Generation: result.Generation, Scope: scopeWire(result.Scope), Name: result.Name, Uplink: counterWire(result.Uplink), Downlink: counterWire(result.Downlink), Source: result.Source, ObservedAt: timestamppb.New(result.ObservedAt), DurationNs: int64(result.Duration)}, nil
}
func (h Handler) ResetTrafficCounter(ctx context.Context, r *v1.ResetTrafficRequest) (*v1.TrafficResetReport, error) {
	return h.reset(ctx, r, domain.ResetExact)
}
func (h Handler) ResetTrafficPair(ctx context.Context, r *v1.ResetTrafficRequest) (*v1.TrafficResetReport, error) {
	return h.reset(ctx, r, domain.ResetPair)
}
func (h Handler) ResetTrafficCounters(ctx context.Context, r *v1.ResetTrafficRequest) (*v1.TrafficResetReport, error) {
	return h.reset(ctx, r, domain.ResetScope)
}
func (h Handler) reset(ctx context.Context, r *v1.ResetTrafficRequest, kind domain.ResetKind) (*v1.TrafficResetReport, error) {
	if r == nil || r.ExpectedRevision == nil || (kind != domain.ResetExact && r.Direction != v1.Direction_DIRECTION_UNSPECIFIED) {
		return nil, failure(domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Service.Reset(ctx, r.InstanceId, domain.ResetRequest{Epoch: r.Epoch, OperationID: r.OperationId, ExpectedRevision: *r.ExpectedRevision, Kind: kind, Selection: domain.Selection{Scope: scope(r.Scope), Name: r.Name, Direction: direction(r.Direction)}, MaxCounters: r.MaxCounters})
	if err != nil {
		return nil, failure(err)
	}
	return resetWire(result), nil
}
func (h Handler) GetTrafficResetStatus(ctx context.Context, r *v1.GetTrafficResetStatusRequest) (*v1.TrafficResetReport, error) {
	if r == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	result, err := h.Service.ResetStatus(ctx, r.InstanceId, r.Revision, r.Offset)
	if err != nil {
		return nil, failure(err)
	}
	return resetWire(result), nil
}
func resetWire(r domain.ResetReport) *v1.TrafficResetReport {
	result := &v1.TrafficResetReport{InstanceId: r.InstanceID, Epoch: r.Epoch, OperationId: r.OperationID, Revision: r.Revision, Generation: r.Generation, Kind: string(r.Kind), Scope: scopeWire(r.Scope), Name: r.Name, Direction: directionWire(r.Direction), Outcome: r.Outcome, Error: r.Error, RecoveryRequired: r.RecoveryRequired, Total: r.Total, NextOffset: r.NextOffset}
	if !r.StartedAt.IsZero() {
		result.StartedAt = timestamppb.New(r.StartedAt)
	}
	if r.CompletedAt != nil {
		result.CompletedAt = timestamppb.New(*r.CompletedAt)
	}
	for _, item := range r.Items {
		value := &v1.ResetCounterResult{Name: item.Selection.Name, Direction: directionWire(item.Selection.Direction), Attempted: item.Attempted, Effect: string(item.Effect), PreviousBytes: item.PreviousBytes, Error: item.Error}
		if item.AcknowledgedAt != nil {
			value.AcknowledgedAt = timestamppb.New(*item.AcknowledgedAt)
		}
		result.Items = append(result.Items, value)
	}
	return result
}
