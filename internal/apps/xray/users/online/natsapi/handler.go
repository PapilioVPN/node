// Package natsapi adapts exact online reads for broker-authorized controllers.
package natsapi

import (
	"context"
	"encoding/json"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/users/online/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/users/online/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/online/infra/jsonwire"
	"github.com/nats-io/nats.go"
)

type Handler struct {
	Service *services.Service
	Timeout time.Duration
}

func deadline(ctx context.Context, headers nats.Header, timeout time.Duration) (context.Context, context.CancelFunc, error) {
	end := time.Now().Add(timeout)
	if values, ok := headers["Papilio-Deadline"]; ok {
		if len(values) != 1 {
			return nil, nil, domain.ErrInvalidArgument
		}
		requested, err := time.Parse(time.RFC3339Nano, values[0])
		if err != nil {
			return nil, nil, domain.ErrInvalidArgument
		}
		if requested.Before(end) {
			end = requested
		}
	}
	ctx, cancel := context.WithDeadline(ctx, end)
	return ctx, cancel, nil
}
func (h Handler) Count(ctx context.Context, data []byte, headers nats.Header) []byte {
	s, err := jsonwire.DecodeSelection(data)
	if err != nil {
		return failure(err)
	}
	ctx, cancel, err := deadline(ctx, headers, h.Timeout)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	result, err := h.Service.Count(ctx, s)
	if err != nil {
		return failure(err)
	}
	data, err = jsonwire.EncodeStatus(result)
	if err != nil {
		return failure(err)
	}
	return data
}
func (h Handler) IPs(ctx context.Context, data []byte, headers nats.Header) []byte {
	return h.page(ctx, data, headers, h.Service.PageIPs, true)
}
func (h Handler) List(ctx context.Context, data []byte, headers nats.Header) []byte {
	return h.page(ctx, data, headers, h.Service.List, false)
}
func (h Handler) Records(ctx context.Context, data []byte, headers nats.Header) []byte {
	return h.page(ctx, data, headers, h.Service.Records, false)
}
func failure(err error) []byte {
	data, _ := json.Marshal(struct {
		Error string `json:"error"`
	}{jsonwire.ErrorCode(err)})
	return data
}
