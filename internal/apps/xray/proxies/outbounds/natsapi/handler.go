// Package natsapi exposes outbound observations to broker-authorized controllers.
package natsapi

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/infra/jsonwire"
	"github.com/nats-io/nats.go"
)

type Handler struct {
	Service *services.Service
	Sources *services.Sources
	Timeout time.Duration
}

func (h Handler) request(ctx context.Context, data []byte, headers nats.Header) (context.Context, context.CancelFunc, string, error) {
	id, err := jsonwire.DecodeID(data)
	if err != nil {
		return nil, nil, id, err
	}
	ctx, cancel, err := h.requestContext(ctx, headers)
	return ctx, cancel, id, err
}
func (h Handler) requestContext(ctx context.Context, headers nats.Header) (context.Context, context.CancelFunc, error) {
	deadline := time.Now().Add(h.Timeout)
	if values, ok := headers["Papilio-Deadline"]; ok {
		if len(values) != 1 {
			return nil, nil, domain.ErrInvalidArgument
		}
		requested, err := time.Parse(time.RFC3339Nano, values[0])
		if err != nil {
			return nil, nil, domain.ErrInvalidArgument
		}
		if requested.Before(deadline) {
			deadline = requested
		}
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	return ctx, cancel, nil
}

func (h Handler) Records(ctx context.Context, data []byte, headers nats.Header) []byte {
	ctx, cancel, id, err := h.request(ctx, data, headers)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	result, err := h.Service.Records(ctx, id)
	if err != nil {
		return failure(err)
	}
	encoded, err := jsonwire.Encode(result)
	if err != nil {
		return failure(err)
	}
	return encoded
}
func errorCode(err error) string {
	code := "unavailable"
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		code = "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		code = "canceled"
	case errors.Is(err, domain.ErrInvalidArgument):
		code = "invalid_argument"
	case errors.Is(err, domain.ErrNotFound):
		code = "not_found"
	case errors.Is(err, domain.ErrUnsupported), errors.Is(err, domain.ErrNotRunning):
		code = "failed_precondition"
	case errors.Is(err, domain.ErrUncertain):
		code = "restart_required"
	case errors.Is(err, domain.ErrExecutionChanged):
		code = "execution_changed"
	case errors.Is(err, domain.ErrCapacity):
		code = "resource_exhausted"
	}
	return code
}
func failure(err error) []byte {
	data, _ := json.Marshal(struct {
		Error string `json:"error"`
	}{errorCode(err)})
	return data
}
