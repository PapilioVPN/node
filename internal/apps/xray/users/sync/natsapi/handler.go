// Package natsapi adapts sync requests from broker-authorized controllers.
package natsapi

import (
	"context"
	"encoding/json"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/app/ports"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/domain"
	v1 "github.com/PapilioVPN/node/internal/apps/xray/users/sync/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/infra/wire"
	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"
)

type Handler struct {
	Service   ports.Operations
	Transfers *services.Transfers
	Timeout   time.Duration
}

func failure(err error) []byte {
	code := wire.Code(err)
	data, _ := json.Marshal(map[string]any{"error": map[string]string{"code": code, "message": code}})
	return data
}
func send(message proto.Message, err error) []byte {
	if err != nil {
		return failure(err)
	}
	data, err := wire.Encode(message)
	if err != nil {
		return failure(err)
	}
	return data
}
func receipt(p domain.ReceiptPage, err error) []byte {
	if p.Receipt.OperationID == "" && err != nil {
		return failure(err)
	}
	return send(wire.Result(p, err), nil)
}
func (h Handler) request(ctx context.Context, data []byte, headers nats.Header, message proto.Message) (context.Context, context.CancelFunc, error) {
	if err := wire.Decode(data, message); err != nil {
		return nil, nil, err
	}
	deadline := time.Now().Add(h.Timeout)
	if values, ok := headers["Papilio-Deadline"]; ok {
		if len(values) != 1 {
			return nil, nil, domain.ErrInvalid
		}
		requested, err := time.Parse(time.RFC3339Nano, values[0])
		if err != nil {
			return nil, nil, domain.ErrInvalid
		}
		if requested.Before(deadline) {
			deadline = requested
		}
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	return ctx, cancel, nil
}
func (h Handler) Revision(ctx context.Context, data []byte, headers nats.Header) []byte {
	var r v1.InstanceRequest
	ctx, cancel, err := h.request(ctx, data, headers, &r)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	revision, err := h.Service.Revision(ctx, r.InstanceId)
	return send(wire.Revision(revision), err)
}
func (h Handler) Preview(ctx context.Context, data []byte, headers nats.Header) []byte {
	var r v1.SyncRequest
	ctx, cancel, err := h.request(ctx, data, headers, &r)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	input, err := wire.Request(ctx, &r)
	if err != nil {
		return failure(err)
	}
	p, err := h.Service.Preview(ctx, input)
	return send(wire.Preview(p), err)
}
func (h Handler) Apply(ctx context.Context, data []byte, headers nats.Header) []byte {
	var r v1.ApplyRequest
	ctx, cancel, err := h.request(ctx, data, headers, &r)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	revision, err := wire.ReadRevision(r.Expected)
	if err != nil {
		return failure(err)
	}
	p, err := h.Service.Apply(ctx, r.InstanceId, r.PlanId, revision)
	return receipt(p, err)
}
func (h Handler) Status(ctx context.Context, data []byte, headers nats.Header) []byte {
	var r v1.StatusRequest
	ctx, cancel, err := h.request(ctx, data, headers, &r)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	p, err := h.Service.Status(ctx, r.InstanceId, r.OperationId, int(r.Offset))
	return receipt(p, err)
}
func (h Handler) Refresh(ctx context.Context, data []byte, headers nats.Header) []byte {
	var r v1.OperationRequest
	ctx, cancel, err := h.request(ctx, data, headers, &r)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	p, err := h.Service.Refresh(ctx, r.InstanceId, r.OperationId)
	return receipt(p, err)
}
func (h Handler) Recover(ctx context.Context, data []byte, headers nats.Header) []byte {
	var r v1.RecoveryRequest
	ctx, cancel, err := h.request(ctx, data, headers, &r)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	if r.ExpectedConfig == nil {
		return failure(domain.ErrInvalid)
	}
	p, err := h.Service.Recover(ctx, r.InstanceId, r.OperationId, *r.ExpectedConfig)
	return receipt(p, err)
}
func (h Handler) Begin(ctx context.Context, data []byte, headers nats.Header) []byte {
	var r v1.BeginRequest
	ctx, cancel, err := h.request(ctx, data, headers, &r)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	input, err := wire.Request(ctx, r.Request)
	if err != nil {
		return failure(err)
	}
	st, err := h.Transfers.Begin(ctx, input, int(r.Items), int(r.Bytes))
	return send(wire.Transfer(st), err)
}
func (h Handler) Append(ctx context.Context, data []byte, headers nats.Header) []byte {
	var r v1.ChunkRequest
	ctx, cancel, err := h.request(ctx, data, headers, &r)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	chunk, err := wire.Chunk(&r)
	if err != nil {
		return failure(err)
	}
	st, err := h.Transfers.Append(ctx, r.InstanceId, r.TransferId, chunk)
	return send(wire.Transfer(st), err)
}
func (h Handler) Finish(ctx context.Context, data []byte, headers nats.Header) []byte {
	var r v1.TransferRequest
	ctx, cancel, err := h.request(ctx, data, headers, &r)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	input, err := h.Transfers.Finish(ctx, r.InstanceId, r.TransferId)
	if err != nil {
		return failure(err)
	}
	p, err := h.Service.Preview(ctx, input)
	return send(wire.Preview(p), err)
}
func (h Handler) Discard(ctx context.Context, data []byte, headers nats.Header) []byte {
	var r v1.TransferRequest
	ctx, cancel, err := h.request(ctx, data, headers, &r)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	return send(&v1.Empty{}, h.Transfers.Discard(ctx, r.InstanceId, r.TransferId))
}
