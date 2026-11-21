// Package natsapi exposes live inbound operations to broker-authorized controllers.
package natsapi

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/infra/jsonwire"
	"github.com/nats-io/nats.go"
)

type Handler struct {
	Service *services.Service
	Timeout time.Duration
}

func (h Handler) request(ctx context.Context, data []byte, headers nats.Header) (context.Context, context.CancelFunc, jsonwire.Request, error) {
	input, err := jsonwire.Decode(data)
	if err != nil {
		return nil, nil, input, err
	}
	if input.InstanceID == nil {
		return nil, nil, input, domain.ErrInvalidArgument
	}
	deadline := time.Now().Add(h.Timeout)
	if values, ok := headers["Papilio-Deadline"]; ok {
		if len(values) != 1 {
			return nil, nil, input, domain.ErrInvalidArgument
		}
		requested, err := time.Parse(time.RFC3339Nano, values[0])
		if err != nil {
			return nil, nil, input, domain.ErrInvalidArgument
		}
		if requested.Before(deadline) {
			deadline = requested
		}
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	return ctx, cancel, input, nil
}
func (h Handler) Records(ctx context.Context, data []byte, headers nats.Header) []byte {
	ctx, cancel, input, err := h.request(ctx, data, headers)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	if input.Tag != nil || input.HasConfiguration {
		return failure(domain.ErrInvalidArgument)
	}
	result, err := h.Service.Records(ctx, *input.InstanceID)
	if err != nil {
		return failure(err)
	}
	encoded, err := jsonwire.EncodeRecords(result)
	return response(encoded, err)
}
func (h Handler) Tags(ctx context.Context, data []byte, headers nats.Header) []byte {
	ctx, cancel, input, err := h.request(ctx, data, headers)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	if input.Tag != nil || input.HasConfiguration {
		return failure(domain.ErrInvalidArgument)
	}
	result, err := h.Service.Tags(ctx, *input.InstanceID)
	if err != nil {
		return failure(err)
	}
	encoded, err := jsonwire.EncodeTags(result)
	return response(encoded, err)
}
func (h Handler) Status(ctx context.Context, data []byte, headers nats.Header) []byte {
	ctx, cancel, input, err := h.request(ctx, data, headers)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	if input.Tag != nil || input.HasConfiguration {
		return failure(domain.ErrInvalidArgument)
	}
	result, err := h.Service.Status(ctx, *input.InstanceID)
	if err != nil {
		return failure(err)
	}
	encoded, err := jsonwire.EncodeStatus(result)
	return response(encoded, err)
}
func (h Handler) Add(ctx context.Context, data []byte, headers nats.Header) []byte {
	ctx, cancel, input, err := h.request(ctx, data, headers)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	if input.Tag != nil || !input.HasConfiguration {
		return failure(domain.ErrInvalidArgument)
	}
	result, err := h.Service.Add(ctx, *input.InstanceID, input.Configuration)
	return mutation(result, err)
}
func (h Handler) Remove(ctx context.Context, data []byte, headers nats.Header) []byte {
	ctx, cancel, input, err := h.request(ctx, data, headers)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	if input.Tag == nil || input.HasConfiguration {
		return failure(domain.ErrInvalidArgument)
	}
	result, err := h.Service.Remove(ctx, *input.InstanceID, *input.Tag)
	return mutation(result, err)
}
func mutation(result domain.Operation, err error) []byte {
	code := ""
	if err != nil {
		code = errorCode(err)
	}
	data, err := jsonwire.EncodeOperation(result, code)
	return response(data, err)
}
func response(data []byte, err error) []byte {
	if err != nil {
		return failure(err)
	}
	return data
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
