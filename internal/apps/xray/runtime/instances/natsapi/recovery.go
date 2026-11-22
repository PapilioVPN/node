package natsapi

import (
	"context"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/jsonwire"
	"github.com/nats-io/nats.go"
)

func (h Handler) recoveryInput(ctx context.Context, data []byte, headers nats.Header, fields, optional string) (context.Context, context.CancelFunc, jsonwire.Input, error) {
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	if values, ok := headers["Papilio-Deadline"]; ok {
		if len(values) != 1 {
			return ctx, cancel, jsonwire.Input{}, domain.ErrInvalidArgument
		}
		deadline, err := time.Parse(time.RFC3339Nano, values[0])
		if err != nil {
			return ctx, cancel, jsonwire.Input{}, domain.ErrInvalidArgument
		}
		shorter, release := context.WithDeadline(ctx, deadline)
		parent := cancel
		cancel = func() { release(); parent() }
		ctx = shorter
	}
	q, err := jsonwire.Decode(data, fields, optional)
	return ctx, cancel, q, err
}
func (h Handler) List(ctx context.Context, data []byte, headers nats.Header) []byte {
	ctx, cancel, q, err := h.recoveryInput(ctx, data, headers, "after limit", "after limit")
	defer cancel()
	if err != nil {
		return failure(err, nil)
	}
	r, err := h.Service.List(ctx, q.After, q.Limit)
	if err != nil {
		return failure(err, nil)
	}
	return encode(pageResult(r))
}
func (h Handler) Lease(ctx context.Context, data []byte, headers nats.Header) []byte {
	ctx, cancel, id, err := h.request(ctx, data, headers)
	defer cancel()
	if err != nil {
		return failure(err, nil)
	}
	r, err := h.Service.Lease(ctx, id)
	if err != nil {
		return failure(err, nil)
	}
	return encode(jsonwire.Lease(r))
}
func (h Handler) Retention(ctx context.Context, data []byte, headers nats.Header) []byte {
	ctx, cancel, q, err := h.recoveryInput(ctx, data, headers, "instance_id execution_id preserve", "")
	defer cancel()
	if err != nil {
		return failure(err, nil)
	}
	id := q.ID
	r, err := h.Service.SetRetention(ctx, id, domain.RetentionRequest{ExecutionID: q.ExecutionID, Preserve: q.Preserve})
	if err != nil {
		return failure(err, &r)
	}
	return encode(controlResult(r))
}
func (h Handler) Repair(ctx context.Context, data []byte, headers nats.Header) []byte {
	ctx, cancel, q, err := h.recoveryInput(ctx, data, headers, "instance_id execution_id max_attempts replace_unready", "execution_id replace_unready")
	defer cancel()
	if err != nil {
		return failure(err, nil)
	}
	id := q.ID
	r, err := h.Service.Repair(ctx, id, domain.RepairRequest{ExecutionID: q.ExecutionID, MaxAttempts: q.MaxAttempts, ReplaceUnready: q.ReplaceUnready})
	if err != nil {
		return failureRepair(err, &r.Control, &r)
	}
	return encode(repairResult(r))
}
func (h Handler) AcquireLease(ctx context.Context, data []byte, headers nats.Header) []byte {
	ctx, cancel, q, err := h.recoveryInput(ctx, data, headers, "instance_id expected_revision execution_id owner ttl_seconds policy", "")
	defer cancel()
	if err != nil {
		return failure(err, nil)
	}
	id := q.ID
	revision, err := jsonwire.Revision(q.ExpectedRevision)
	if err != nil {
		return failure(err, nil)
	}
	r, err := h.Service.AcquireLease(ctx, id, domain.AcquireLease{ExpectedRevision: revision, ExecutionID: q.ExecutionID, Owner: q.Owner, TTLSeconds: q.TTLSeconds, Policy: q.Policy})
	if err != nil {
		return failure(err, nil)
	}
	return encode(jsonwire.Receipt(r))
}
func (h Handler) RenewLease(ctx context.Context, data []byte, headers nats.Header) []byte {
	ctx, cancel, q, err := h.recoveryInput(ctx, data, headers, "instance_id expected_revision token ttl_seconds", "")
	defer cancel()
	if err != nil {
		return failure(err, nil)
	}
	id := q.ID
	revision, err := jsonwire.Revision(q.ExpectedRevision)
	if err != nil {
		return failure(err, nil)
	}
	r, err := h.Service.RenewLease(ctx, id, domain.RenewLease{ExpectedRevision: revision, Token: q.Token, TTLSeconds: q.TTLSeconds})
	if err != nil {
		return failure(err, nil)
	}
	return encode(jsonwire.Lease(r))
}
func (h Handler) ReleaseLease(ctx context.Context, data []byte, headers nats.Header) []byte {
	ctx, cancel, q, err := h.recoveryInput(ctx, data, headers, "instance_id expected_revision token", "")
	defer cancel()
	if err != nil {
		return failure(err, nil)
	}
	id := q.ID
	revision, err := jsonwire.Revision(q.ExpectedRevision)
	if err != nil {
		return failure(err, nil)
	}
	r, err := h.Service.ReleaseLease(ctx, id, domain.ReleaseLease{ExpectedRevision: revision, Token: q.Token})
	if err != nil {
		return failure(err, nil)
	}
	return encode(jsonwire.Lease(r))
}

type pageResponse struct {
	Items     []statusResponse `json:"items"`
	NextAfter string           `json:"next_after"`
}

func pageResult(r domain.InstancePage) pageResponse {
	out := pageResponse{Items: make([]statusResponse, 0, len(r.Items)), NextAfter: r.NextAfter}
	for _, item := range r.Items {
		out.Items = append(out.Items, statusResult(item))
	}
	return out
}

type repairResponse struct {
	Control  controlResponse `json:"control"`
	Attempts uint32          `json:"attempts"`
	Ready    bool            `json:"ready"`
	Action   string          `json:"action"`
}

func repairResult(r domain.RepairResult) repairResponse {
	return repairResponse{Control: controlResult(r.Control), Attempts: r.Attempts, Ready: r.Ready, Action: r.Action}
}
