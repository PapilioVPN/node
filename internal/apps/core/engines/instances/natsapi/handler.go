// Package natsapi exposes instance operations over broker-authorized Core NATS.
package natsapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/PapilioVPN/node/internal/apps/core/engines/instances/app/services"
	"github.com/PapilioVPN/node/internal/apps/core/engines/instances/domain"
	"github.com/nats-io/nats.go"
)

type Handler struct {
	Service *services.Service
	Timeout time.Duration
}

func (h Handler) Status(ctx context.Context, data []byte, headers nats.Header) []byte {
	ctx, cancel, id, err := h.request(ctx, data, headers)
	defer cancel()
	if err != nil {
		return failure(err, nil)
	}
	result, err := h.Service.Status(ctx, id)
	if err != nil {
		return failure(err, nil)
	}
	return encode(statusResult(result))
}
func (h Handler) Start(ctx context.Context, data []byte, headers nats.Header) []byte {
	return h.control(ctx, data, headers, h.Service.Start)
}
func (h Handler) Stop(ctx context.Context, data []byte, headers nats.Header) []byte {
	return h.control(ctx, data, headers, h.Service.Stop)
}
func (h Handler) Restart(ctx context.Context, data []byte, headers nats.Header) []byte {
	return h.control(ctx, data, headers, h.Service.Restart)
}
func (h Handler) control(ctx context.Context, data []byte, headers nats.Header, operation func(context.Context, string) (domain.Control, error)) []byte {
	ctx, cancel, id, err := h.request(ctx, data, headers)
	defer cancel()
	if err != nil {
		return failure(err, &domain.Control{Effect: domain.NotApplied})
	}
	result, err := operation(ctx, id)
	if err != nil {
		return failure(err, &result)
	}
	return encode(controlResult(result))
}
func (h Handler) Check(ctx context.Context, data []byte, headers nats.Header) []byte {
	ctx, cancel, id, err := h.request(ctx, data, headers)
	defer cancel()
	if err != nil {
		return failure(err, nil)
	}
	result, err := h.Service.Check(ctx, id)
	if err != nil {
		return failure(err, nil)
	}
	return encode(readinessResult(result))
}

func (h Handler) request(ctx context.Context, data []byte, headers nats.Header) (context.Context, context.CancelFunc, string, error) {
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	if len(data) > 2048 {
		return ctx, cancel, "", domain.ErrInvalidArgument
	}
	// Exactly one key: this also rejects duplicate identities rather than accepting
	// whichever field a JSON decoder happens to retain.
	decoder := json.NewDecoder(bytes.NewReader(data))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return ctx, cancel, "", domain.ErrInvalidArgument
	}
	key, err := decoder.Token()
	if err != nil || key != "instance_id" {
		return ctx, cancel, "", domain.ErrInvalidArgument
	}
	var id string
	if err := decoder.Decode(&id); err != nil {
		return ctx, cancel, "", domain.ErrInvalidArgument
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') {
		return ctx, cancel, "", domain.ErrInvalidArgument
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return ctx, cancel, "", domain.ErrInvalidArgument
	}
	if values, ok := headers["Papilio-Deadline"]; ok {
		if len(values) != 1 {
			return ctx, cancel, "", domain.ErrInvalidArgument
		}
		deadline, err := time.Parse(time.RFC3339Nano, values[0])
		if err != nil {
			return ctx, cancel, "", domain.ErrInvalidArgument
		}
		shorter, release := context.WithDeadline(ctx, deadline)
		parentCancel := cancel
		cancel = func() { release(); parentCancel() }
		ctx = shorter
	}
	return ctx, cancel, id, nil
}

func encode(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		return []byte(`{"error":"unavailable"}`)
	}
	return data
}
func failure(err error, result *domain.Control) []byte {
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
	case errors.Is(err, domain.ErrBusy):
		code = "resource_exhausted"
	case errors.Is(err, domain.ErrClosed), errors.Is(err, domain.ErrUnsupported):
		code = "failed_precondition"
	}
	response := errorResponse{Error: code}
	if result != nil {
		value := controlResult(*result)
		response.Control = &value
	}
	return encode(response)
}
