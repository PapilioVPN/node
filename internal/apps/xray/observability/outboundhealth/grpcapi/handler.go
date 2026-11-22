// Package grpcapi exposes the versioned native outbound observation contract.
package grpcapi

import (
	"context"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/outboundhealth/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/outboundhealth/domain"
	healthv1 "github.com/PapilioVPN/node/internal/apps/xray/observability/outboundhealth/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/outboundhealth/infra/jsonwire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Handler struct {
	healthv1.UnimplementedOutboundHealthServiceServer
	Service *services.Service
	Timeout time.Duration
}

func (h Handler) Register(registrar grpc.ServiceRegistrar) {
	healthv1.RegisterOutboundHealthServiceServer(registrar, h)
}
func (h Handler) GetOutboundStatus(ctx context.Context, r *healthv1.GetOutboundStatusRequest) (*healthv1.OutboundObservation, error) {
	if r == nil || r.OutboundTag == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	o, err := h.Service.Get(ctx, r.InstanceId, domain.Selection{Tag: *r.OutboundTag, MaxSampleAgeSeconds: r.MaxSampleAgeSeconds})
	if err != nil {
		return nil, failure(err)
	}
	result := &healthv1.OutboundObservation{Observation: metadata(o.InstanceID, o.Generation, o.Source, o.ObservedAt, o.Duration, o.MaxSampleAgeSeconds)}
	if o.Status != nil {
		result.Status = present(*o.Status)
	}
	return result, nil
}
func (h Handler) ListOutboundStatuses(ctx context.Context, r *healthv1.ListOutboundStatusesRequest) (*healthv1.OutboundStatusesPage, error) {
	if r == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	p, err := h.Service.List(ctx, r.InstanceId, domain.PageRequest{PageSize: r.PageSize, PageToken: r.PageToken, MaxSampleAgeSeconds: r.MaxSampleAgeSeconds})
	if err != nil {
		return nil, failure(err)
	}
	result := &healthv1.OutboundStatusesPage{Observation: metadata(p.InstanceID, p.Generation, p.Source, p.ObservedAt, p.Duration, p.MaxSampleAgeSeconds), Statuses: make([]*healthv1.OutboundStatus, 0, len(p.Statuses)), NextPageToken: p.NextPageToken, ServedAt: timestamppb.New(p.ServedAt), Total: p.Total}
	if p.ExpiresAt != nil {
		result.ExpiresAt = timestamppb.New(*p.ExpiresAt)
	}
	for _, v := range p.Statuses {
		result.Statuses = append(result.Statuses, present(v))
	}
	return result, nil
}
func metadata(id string, generation uint64, source string, at time.Time, duration time.Duration, age uint32) *healthv1.ObservationMetadata {
	return &healthv1.ObservationMetadata{InstanceId: id, Generation: generation, Source: source, ObservedAt: timestamppb.New(at), DurationNs: int64(duration), MaxSampleAgeSeconds: age}
}
func present(v domain.Status) *healthv1.OutboundStatus {
	s := &healthv1.OutboundStatus{OutboundTag: v.Tag, Alive: v.Alive, DelayMs: v.DelayMS, LatencyMs: v.LatencyMS, LastSeenUnixSec: v.LastSeenUnixSec, LastTryUnixSec: v.LastTryUnixSec, Mode: v.Mode, SampleAgeSeconds: v.SampleAgeSeconds, Freshness: v.Freshness}
	if v.SampledAt != nil {
		s.SampledAt = timestamppb.New(*v.SampledAt)
	}
	if h := v.HealthPing; h != nil {
		s.HealthPing = &healthv1.HealthPing{All: h.All, Fail: h.Fail, DeviationNs: h.DeviationNS, AverageNs: h.AverageNS, MaxNs: h.MaxNS, MinNs: h.MinNS}
	}
	return s
}
func failure(err error) error {
	message := jsonwire.ErrorCode(err)
	code := codes.Unavailable
	switch message {
	case "invalid_argument":
		code = codes.InvalidArgument
	case "not_found":
		code = codes.NotFound
	case "deadline_exceeded":
		code = codes.DeadlineExceeded
	case "canceled":
		code = codes.Canceled
	case "api_not_configured", "unsupported", "not_running", "closed", "continuation_expired":
		code = codes.FailedPrecondition
	case "execution_changed":
		code = codes.Aborted
	case "resource_exhausted":
		code = codes.ResourceExhausted
	}
	return status.Error(code, message)
}
