package grpcapi

import (
	"context"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	v1 "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/grpcapi/proto/v1"
	"google.golang.org/grpc/status"
)

func (h Handler) ListInstances(ctx context.Context, q *v1.ListInstancesRequest) (*v1.InstancePage, error) {
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	r, err := h.Service.List(ctx, q.GetAfter(), q.GetLimit())
	if err != nil {
		return nil, operationError(err)
	}
	out := &v1.InstancePage{NextAfter: r.NextAfter}
	for _, item := range r.Items {
		out.Items = append(out.Items, statusResult(item))
	}
	return out, nil
}
func (h Handler) RepairInstance(ctx context.Context, q *v1.RepairInstanceRequest) (*v1.RepairResult, error) {
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	r, err := h.Service.Repair(ctx, q.GetInstanceId(), domain.RepairRequest{ExecutionID: q.GetExecutionId(), MaxAttempts: q.GetMaxAttempts(), ReplaceUnready: q.GetReplaceUnready()})
	out := &v1.RepairResult{Control: controlResult(r.Control), Attempts: r.Attempts, Ready: r.Ready, Action: r.Action}
	if err != nil {
		detailed, detailErr := status.Convert(operationError(err)).WithDetails(out)
		if detailErr != nil {
			return nil, operationError(err)
		}
		return nil, detailed.Err()
	}
	return out, nil
}
func (h Handler) SetInstanceRetention(ctx context.Context, q *v1.SetInstanceRetentionRequest) (*v1.ControlResult, error) {
	return h.control(ctx, q.GetInstanceId(), func(ctx context.Context, id string) (domain.Control, error) {
		return h.Service.SetRetention(ctx, id, domain.RetentionRequest{ExecutionID: q.GetExecutionId(), Preserve: q.GetPreserve()})
	})
}
func (h Handler) GetInstanceLease(ctx context.Context, q *v1.GetInstanceLeaseRequest) (*v1.LeaseStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	r, err := h.Service.Lease(ctx, q.GetInstanceId())
	if err != nil {
		return nil, operationError(err)
	}
	return leaseResult(r), nil
}
func (h Handler) AcquireInstanceLease(ctx context.Context, q *v1.AcquireInstanceLeaseRequest) (*v1.LeaseReceipt, error) {
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	r, err := h.Service.AcquireLease(ctx, q.GetInstanceId(), domain.AcquireLease{ExpectedRevision: q.GetExpectedRevision(), ExecutionID: q.GetExecutionId(), Owner: q.GetOwner(), TTLSeconds: q.GetTtlSeconds(), Policy: q.GetPolicy()})
	if err != nil {
		return nil, operationError(err)
	}
	return &v1.LeaseReceipt{Status: leaseResult(r.Status), Token: r.Token}, nil
}
func (h Handler) RenewInstanceLease(ctx context.Context, q *v1.RenewInstanceLeaseRequest) (*v1.LeaseStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	r, err := h.Service.RenewLease(ctx, q.GetInstanceId(), domain.RenewLease{ExpectedRevision: q.GetExpectedRevision(), Token: q.GetToken(), TTLSeconds: q.GetTtlSeconds()})
	if err != nil {
		return nil, operationError(err)
	}
	return leaseResult(r), nil
}
func (h Handler) ReleaseInstanceLease(ctx context.Context, q *v1.ReleaseInstanceLeaseRequest) (*v1.LeaseStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	r, err := h.Service.ReleaseLease(ctx, q.GetInstanceId(), domain.ReleaseLease{ExpectedRevision: q.GetExpectedRevision(), Token: q.GetToken()})
	if err != nil {
		return nil, operationError(err)
	}
	return leaseResult(r), nil
}
