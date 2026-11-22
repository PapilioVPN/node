// Package grpcapi exposes typed shareable instance diagnostics.
package grpcapi

import (
	"context"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/tools/diagnostics/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/diagnostics/domain"
	diagnosticv1 "github.com/PapilioVPN/node/internal/apps/xray/tools/diagnostics/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/diagnostics/infra/jsonwire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Handler struct {
	diagnosticv1.UnimplementedDiagnosticsServiceServer
	Service *services.Service
	Timeout time.Duration
}

func (h Handler) Register(r grpc.ServiceRegistrar) {
	diagnosticv1.RegisterDiagnosticsServiceServer(r, h)
}
func (h Handler) GetInstanceDiagnostics(ctx context.Context, input *diagnosticv1.GetInstanceDiagnosticsRequest) (*diagnosticv1.DiagnosticReport, error) {
	if input == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	r, err := h.Service.Report(ctx, domain.Request{InstanceID: input.InstanceId, MaxAgeSeconds: input.MaxAgeSeconds})
	if err != nil {
		return nil, failure(err)
	}
	e, c, b, n := r.Execution, r.Configuration, r.Binary, r.Native
	result := &diagnosticv1.DiagnosticReport{InstanceId: r.InstanceID, ObservedAt: timestamppb.New(r.ObservedAt), DurationNs: int64(r.Duration), MaxAgeSeconds: r.MaxAgeSeconds, Consistency: r.Consistency,
		Execution:     &diagnosticv1.ExecutionEvidence{Evidence: evidence(e.Evidence), LifecycleState: e.State, Generation: e.Generation, AppliedRevision: e.AppliedRevision, StartedAt: timestamp(e.StartedAt), ExitedAt: timestamp(e.ExitedAt), ExitCode: e.ExitCode, FailureReason: e.FailureReason, CleanupReason: e.CleanupReason},
		Configuration: &diagnosticv1.ConfigurationEvidence{Evidence: evidence(c.Evidence), DesiredRevision: c.DesiredRevision, UpdatedAt: timestamp(c.UpdatedAt), Durable: c.Durable, MatchesApplied: c.MatchesApplied},
		Binary:        &diagnosticv1.BinaryEvidence{Evidence: evidence(b.Evidence), Version: b.Version, Build: b.Build, GoVersion: b.GoVersion, Os: b.OS, Architecture: b.Architecture, Scope: b.Scope},
		NativeApi:     &diagnosticv1.NativeEvidence{Evidence: evidence(n.Evidence), Generation: n.Generation, Runtime: evidence(n.Runtime), Discovery: evidence(n.Discovery), UptimeSeconds: n.UptimeSeconds, Capabilities: make([]*diagnosticv1.Capability, 0, len(n.Capabilities))},
	}
	for _, c := range n.Capabilities {
		result.NativeApi.Capabilities = append(result.NativeApi.Capabilities, &diagnosticv1.Capability{Name: c.Name, State: c.State, Basis: c.Basis})
	}
	if proto.Size(result) > domain.MaxReportBytes {
		return nil, failure(domain.ErrCapacity)
	}
	return result, nil
}
func evidence(e domain.Evidence) *diagnosticv1.Evidence {
	return &diagnosticv1.Evidence{State: e.State, Reason: e.Reason, Source: e.Source, ObservedAt: timestamp(e.ObservedAt), Freshness: e.Freshness}
}
func timestamp(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return timestamppb.New(*t)
}
func failure(err error) error {
	message := jsonwire.ErrorCode(err)
	code := codes.Unavailable
	switch message {
	case "invalid_argument":
		code = codes.InvalidArgument
	case "not_found":
		code = codes.NotFound
	case "resource_exhausted":
		code = codes.ResourceExhausted
	case "deadline_exceeded":
		code = codes.DeadlineExceeded
	case "canceled":
		code = codes.Canceled
	}
	return status.Error(code, message)
}
