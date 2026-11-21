// Package natsapi exposes bounded logger maintenance to trusted controllers.
package natsapi

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/loggers/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/loggers/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/loggers/infra/jsonwire"
	"github.com/nats-io/nats.go"
)

type Handler struct {
	Service *services.Service
	Timeout time.Duration
}

func (h Handler) request(ctx context.Context, data []byte, headers nats.Header) (context.Context, context.CancelFunc, string, error) {
	id, err := jsonwire.DecodeRequest(data)
	if err != nil {
		return nil, nil, "", err
	}
	deadline := time.Now().Add(h.Timeout)
	if values, ok := headers["Papilio-Deadline"]; ok {
		if len(values) != 1 {
			return nil, nil, "", domain.ErrInvalidArgument
		}
		requested, err := time.Parse(time.RFC3339Nano, values[0])
		if err != nil {
			return nil, nil, "", domain.ErrInvalidArgument
		}
		if requested.Before(deadline) {
			deadline = requested
		}
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	return ctx, cancel, id, nil
}
func (h Handler) Reopen(ctx context.Context, data []byte, headers nats.Header) []byte {
	ctx, cancel, id, err := h.request(ctx, data, headers)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	result, err := h.Service.Reopen(ctx, id)
	code := ""
	if err != nil {
		code = errorCode(err)
	}
	response, err := jsonwire.EncodeOperation(result, code)
	if err != nil {
		return failure(err)
	}
	return response
}
func (h Handler) Status(ctx context.Context, data []byte, headers nats.Header) []byte {
	ctx, cancel, id, err := h.request(ctx, data, headers)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	result, err := h.Service.Status(ctx, id)
	if err != nil {
		return failure(err)
	}
	response, err := jsonwire.EncodeStatus(result)
	if err != nil {
		return failure(err)
	}
	return response
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
