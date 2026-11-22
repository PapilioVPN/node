// Package grpcapi owns the versioned AssetsService controller contract.
package grpcapi

import (
	"context"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/assets/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/assets/domain"
	v1 "github.com/PapilioVPN/node/internal/apps/xray/tools/assets/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/assets/infra/jsonwire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	"time"
)

type Handler struct {
	v1.UnimplementedAssetsServiceServer
	Service *services.Service
	Timeout time.Duration
}

func (h Handler) Register(r grpc.ServiceRegistrar) { v1.RegisterAssetsServiceServer(r, h) }
func failure(err error) error {
	code := codes.Unavailable
	switch jsonwire.ErrorCode(err) {
	case "invalid_argument":
		code = codes.InvalidArgument
	case "not_found":
		code = codes.NotFound
	case "busy", "resource_exhausted":
		code = codes.ResourceExhausted
	case "conflict":
		code = codes.Aborted
	case "failed_precondition", "native_rejected", "recovery_required":
		code = codes.FailedPrecondition
	case "canceled":
		code = codes.Canceled
	case "deadline_exceeded":
		code = codes.DeadlineExceeded
	}
	return status.Error(code, jsonwire.ErrorCode(err))
}
func state(s domain.State) *v1.State {
	return &v1.State{InstanceId: s.InstanceID, Revision: s.Revision, Active: s.Active, Previous: s.Previous, Pending: s.Pending}
}
func artifact(a domain.Artifact) *v1.Artifact {
	return &v1.Artifact{Sha256: a.SHA256, Bytes: a.Bytes, Entries: a.Entries}
}
func release(r domain.Release) *v1.Release {
	return &v1.Release{Id: r.ID, Version: r.Version, Binary: artifact(r.Binary), Geoip: artifact(r.GeoIP), Geosite: artifact(r.GeoSite), PreparedAt: timestamppb.New(r.PreparedAt)}
}
func receipt(r domain.Receipt, err error) *v1.ActivationResult {
	result := &v1.ActivationResult{State: state(r.State), Outcome: r.Outcome, Ready: r.Ready, ErrorCode: jsonwire.ErrorCode(err)}
	if !r.CompletedAt.IsZero() {
		result.CompletedAt = timestamppb.New(r.CompletedAt)
	}
	return result
}
func (h Handler) InspectAssets(ctx context.Context, q *v1.InspectRequest) (*v1.Inventory, error) {
	if q == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Service.Inspect(ctx, q.InstanceId)
	if err != nil {
		return nil, failure(err)
	}
	response := &v1.Inventory{State: state(result.State), Selected: result.Selected, ObservedAt: timestamppb.New(result.ObservedAt)}
	if result.Installed != nil {
		response.Installed = release(*result.Installed)
	}
	return response, nil
}
func (h Handler) StageAssets(ctx context.Context, q *v1.StageRequest) (*v1.Release, error) {
	if q == nil || q.Candidate == nil || q.Candidate.Binary == nil || q.Candidate.Geoip == nil || q.Candidate.Geosite == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	upload := func(f *v1.Upload) domain.File { return domain.File{Content: f.Content, SHA256: f.Sha256} }
	result, err := h.Service.Stage(ctx, q.InstanceId, domain.Candidate{Binary: upload(q.Candidate.Binary), GeoIP: upload(q.Candidate.Geoip), GeoSite: upload(q.Candidate.Geosite)})
	if err != nil {
		return nil, failure(err)
	}
	return release(result), nil
}
func (h Handler) ActivateAssets(ctx context.Context, q *v1.ActivateRequest) (*v1.ActivationResult, error) {
	if q == nil || q.ExpectedRevision == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Service.Activate(ctx, q.InstanceId, domain.Activation{ExpectedRevision: *q.ExpectedRevision, ExecutionID: q.ExecutionId, ReleaseID: q.ReleaseId})
	return receipt(result, err), nil
}
func (h Handler) RollbackAssets(ctx context.Context, q *v1.RecoveryRequest) (*v1.ActivationResult, error) {
	if q == nil || q.ExpectedRevision == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Service.Rollback(ctx, q.InstanceId, domain.Activation{ExpectedRevision: *q.ExpectedRevision, ExecutionID: q.ExecutionId})
	return receipt(result, err), nil
}
func (h Handler) RecoverAssets(ctx context.Context, q *v1.RecoveryRequest) (*v1.ActivationResult, error) {
	if q == nil || q.ExpectedRevision == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Service.Recover(ctx, q.InstanceId, domain.Activation{ExpectedRevision: *q.ExpectedRevision, ExecutionID: q.ExecutionId})
	return receipt(result, err), nil
}
