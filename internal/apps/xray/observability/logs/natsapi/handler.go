// Package natsapi adapts Core request/reply to bounded log pulls. NATS request
// cancellation is not delivered to Node; Papilio-Deadline bounds abandoned work.
package natsapi

import (
	"context"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/logs/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/logs/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/logs/infra/jsonwire"
	"github.com/nats-io/nats.go"
)

type Handler struct {
	Service *services.Service
	Timeout time.Duration
}

func (h Handler) Tail(ctx context.Context, data []byte, headers nats.Header) []byte {
	return h.serve(ctx, data, headers, false)
}
func (h Handler) Follow(ctx context.Context, data []byte, headers nats.Header) []byte {
	return h.serve(ctx, data, headers, true)
}
func (h Handler) serve(ctx context.Context, data []byte, headers nats.Header, follow bool) []byte {
	input, err := jsonwire.Decode(data, follow)
	if err != nil {
		return jsonwire.Failure(err)
	}
	deadline := time.Now().Add(h.Timeout)
	if values, ok := headers["Papilio-Deadline"]; ok {
		if len(values) != 1 {
			return jsonwire.Failure(domain.ErrInvalidArgument)
		}
		selected, err := time.Parse(time.RFC3339Nano, values[0])
		if err != nil {
			return jsonwire.Failure(domain.ErrInvalidArgument)
		}
		if selected.Before(deadline) {
			deadline = selected
		}
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	var p domain.Page
	if follow {
		p, err = h.Service.Follow(ctx, input.InstanceID, domain.FollowRequest{Cursor: input.Cursor, Limit: input.Limit, WaitMilliseconds: input.WaitMilliseconds})
	} else {
		p, err = h.Service.Tail(ctx, input.InstanceID, domain.TailRequest{Limit: input.Limit})
	}
	if err != nil {
		return jsonwire.Failure(err)
	}
	encoded, err := jsonwire.Encode(p)
	if err != nil {
		return jsonwire.Failure(err)
	}
	return encoded
}
