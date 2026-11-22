// Package grpcapi adapts versioned sync operations to their owning service.
package grpcapi

import (
	"context"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/app/ports"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/domain"
	v1 "github.com/PapilioVPN/node/internal/apps/xray/users/sync/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/infra/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Handler struct {
	v1.UnimplementedSyncServiceServer
	Service   ports.Operations
	Transfers *services.Transfers
	Timeout   time.Duration
}

func (h Handler) Register(r grpc.ServiceRegistrar) { v1.RegisterSyncServiceServer(r, h) }
func failure(err error) error {
	code := wire.Code(err)
	mapped := codes.Unavailable
	switch code {
	case "invalid_argument":
		mapped = codes.InvalidArgument
	case "resource_exhausted":
		mapped = codes.ResourceExhausted
	case "not_found":
		mapped = codes.NotFound
	case "conflict":
		mapped = codes.Aborted
	case "failed_precondition":
		mapped = codes.FailedPrecondition
	case "cancelled":
		mapped = codes.Canceled
	case "deadline_exceeded":
		mapped = codes.DeadlineExceeded
	}
	return status.Error(mapped, code)
}
func result(p domain.ReceiptPage, err error) (*v1.Result, error) {
	if err != nil && p.Receipt.OperationID == "" {
		return nil, failure(err)
	}
	return wire.Result(p, err), nil
}
func (h Handler) GetSyncRevision(ctx context.Context, r *v1.InstanceRequest) (*v1.Revision, error) {
	if r == nil {
		return nil, failure(domain.ErrInvalid)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	revision, err := h.Service.Revision(ctx, r.InstanceId)
	if err != nil {
		return nil, failure(err)
	}
	return wire.Revision(revision), nil
}
func (h Handler) PreviewSync(ctx context.Context, r *v1.SyncRequest) (*v1.Preview, error) {
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	input, err := wire.Request(ctx, r)
	if err != nil {
		return nil, failure(err)
	}
	preview, err := h.Service.Preview(ctx, input)
	if err != nil {
		return nil, failure(err)
	}
	return wire.Preview(preview), nil
}
func (h Handler) ApplySync(ctx context.Context, r *v1.ApplyRequest) (*v1.Result, error) {
	if r == nil {
		return nil, failure(domain.ErrInvalid)
	}
	revision, err := wire.ReadRevision(r.Expected)
	if err != nil {
		return nil, failure(err)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	p, err := h.Service.Apply(ctx, r.InstanceId, r.PlanId, revision)
	return result(p, err)
}
func (h Handler) GetSyncStatus(ctx context.Context, r *v1.StatusRequest) (*v1.Result, error) {
	if r == nil {
		return nil, failure(domain.ErrInvalid)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	p, err := h.Service.Status(ctx, r.InstanceId, r.OperationId, int(r.Offset))
	return result(p, err)
}
func (h Handler) RefreshSync(ctx context.Context, r *v1.OperationRequest) (*v1.Result, error) {
	if r == nil {
		return nil, failure(domain.ErrInvalid)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	p, err := h.Service.Refresh(ctx, r.InstanceId, r.OperationId)
	return result(p, err)
}
func (h Handler) RecoverSync(ctx context.Context, r *v1.RecoveryRequest) (*v1.Result, error) {
	if r == nil || r.ExpectedConfig == nil {
		return nil, failure(domain.ErrInvalid)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	p, err := h.Service.Recover(ctx, r.InstanceId, r.OperationId, *r.ExpectedConfig)
	return result(p, err)
}
func (h Handler) BeginSyncTransfer(ctx context.Context, r *v1.BeginRequest) (*v1.Transfer, error) {
	if r == nil {
		return nil, failure(domain.ErrInvalid)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	input, err := wire.Request(ctx, r.Request)
	if err != nil {
		return nil, failure(err)
	}
	st, err := h.Transfers.Begin(ctx, input, int(r.Items), int(r.Bytes))
	if err != nil {
		return nil, failure(err)
	}
	return wire.Transfer(st), nil
}
func (h Handler) AppendSyncChunk(ctx context.Context, r *v1.ChunkRequest) (*v1.Transfer, error) {
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	c, err := wire.Chunk(r)
	if err != nil {
		return nil, failure(err)
	}
	st, err := h.Transfers.Append(ctx, r.InstanceId, r.TransferId, c)
	if err != nil {
		return nil, failure(err)
	}
	return wire.Transfer(st), nil
}
func (h Handler) PreviewSyncTransfer(ctx context.Context, r *v1.TransferRequest) (*v1.Preview, error) {
	if r == nil {
		return nil, failure(domain.ErrInvalid)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	input, err := h.Transfers.Finish(ctx, r.InstanceId, r.TransferId)
	if err != nil {
		return nil, failure(err)
	}
	p, err := h.Service.Preview(ctx, input)
	if err != nil {
		return nil, failure(err)
	}
	return wire.Preview(p), nil
}
func (h Handler) DiscardSyncTransfer(ctx context.Context, r *v1.TransferRequest) (*v1.Empty, error) {
	if r == nil {
		return nil, failure(domain.ErrInvalid)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	if err := h.Transfers.Discard(ctx, r.InstanceId, r.TransferId); err != nil {
		return nil, failure(err)
	}
	return &v1.Empty{}, nil
}
