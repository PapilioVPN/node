// Package natsapi adapts routing operations to Core NATS request/reply.
package natsapi

import (
	"context"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/infra/jsonwire"
	"github.com/nats-io/nats.go"
)

type Handler struct {
	Service *services.Service
	Timeout time.Duration
}

func (h Handler) List(ctx context.Context, data []byte, headers nats.Header) []byte {
	r, err := jsonwire.DecodeList(data)
	if err != nil {
		return jsonwire.Failure(err)
	}
	ctx, cancel, err := jsonwire.Deadline(ctx, h.Timeout, headers["Papilio-Deadline"])
	if err != nil {
		return jsonwire.Failure(err)
	}
	defer cancel()
	p, err := h.Service.List(ctx, r)
	if err != nil {
		return jsonwire.Failure(err)
	}
	return jsonwire.EncodePage(p)
}
func (h Handler) Add(ctx context.Context, data []byte, headers nats.Header) []byte {
	r, err := jsonwire.DecodeAdd(data)
	if err != nil {
		return jsonwire.Failure(err)
	}
	ctx, cancel, err := jsonwire.Deadline(ctx, h.Timeout, headers["Papilio-Deadline"])
	if err != nil {
		return jsonwire.Failure(err)
	}
	defer cancel()
	p, err := h.Service.Add(ctx, r)
	return jsonwire.EncodeOperation(p, err)
}
func (h Handler) Remove(ctx context.Context, data []byte, headers nats.Header) []byte {
	id, tag, err := jsonwire.DecodeTag(data)
	if err != nil {
		return jsonwire.Failure(err)
	}
	ctx, cancel, err := jsonwire.Deadline(ctx, h.Timeout, headers["Papilio-Deadline"])
	if err != nil {
		return jsonwire.Failure(err)
	}
	defer cancel()
	p, err := h.Service.Remove(ctx, id, tag)
	return jsonwire.EncodeOperation(p, err)
}
func (h Handler) Probe(ctx context.Context, data []byte, headers nats.Header) []byte {
	r, err := jsonwire.DecodeProbe(data)
	if err != nil {
		return jsonwire.Failure(err)
	}
	ctx, cancel, err := jsonwire.Deadline(ctx, h.Timeout, headers["Papilio-Deadline"])
	if err != nil {
		return jsonwire.Failure(err)
	}
	defer cancel()
	p, err := h.Service.Probe(ctx, r)
	return jsonwire.EncodeProbe(p, err)
}
func (h Handler) Balancer(ctx context.Context, data []byte, headers nats.Header) []byte {
	id, tag, err := jsonwire.DecodeTag(data)
	if err != nil {
		return jsonwire.Failure(err)
	}
	ctx, cancel, err := jsonwire.Deadline(ctx, h.Timeout, headers["Papilio-Deadline"])
	if err != nil {
		return jsonwire.Failure(err)
	}
	defer cancel()
	p, err := h.Service.Balancer(ctx, id, tag)
	if err != nil {
		return jsonwire.Failure(err)
	}
	return jsonwire.EncodeBalancer(p)
}
func (h Handler) Override(ctx context.Context, data []byte, headers nats.Header) []byte {
	r, err := jsonwire.DecodeOverride(data)
	if err != nil {
		return jsonwire.Failure(err)
	}
	ctx, cancel, err := jsonwire.Deadline(ctx, h.Timeout, headers["Papilio-Deadline"])
	if err != nil {
		return jsonwire.Failure(err)
	}
	defer cancel()
	p, err := h.Service.Override(ctx, r)
	return jsonwire.EncodeOperation(p, err)
}
func (h Handler) Status(ctx context.Context, data []byte, headers nats.Header) []byte {
	id, err := jsonwire.DecodeID(data)
	if err != nil {
		return jsonwire.Failure(err)
	}
	ctx, cancel, err := jsonwire.Deadline(ctx, h.Timeout, headers["Papilio-Deadline"])
	if err != nil {
		return jsonwire.Failure(err)
	}
	defer cancel()
	p, err := h.Service.Status(ctx, id)
	if err != nil {
		return jsonwire.Failure(err)
	}
	return jsonwire.EncodeStatus(p)
}
