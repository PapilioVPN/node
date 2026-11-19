// Package natsapi exposes bounded physical sensor observations.
package natsapi

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/sensors/app/services"
	"github.com/nats-io/nats.go"
)

type Handler struct {
	Service services.Service
	Timeout time.Duration
}

func (h Handler) Temperatures(ctx context.Context, data []byte, headers nats.Header) []byte {
	ctx, cancel, code := requestContext(ctx, data, headers, h.Timeout)
	if code != "" {
		return failure(code)
	}
	defer cancel()
	result, err := h.Service.Temperatures(ctx)
	if err != nil {
		return readError(err)
	}
	encoded, err := json.Marshal(temperaturesResult(result))
	if err != nil {
		return failure("unavailable")
	}
	return encoded
}
func (h Handler) Fans(ctx context.Context, data []byte, headers nats.Header) []byte {
	ctx, cancel, code := requestContext(ctx, data, headers, h.Timeout)
	if code != "" {
		return failure(code)
	}
	defer cancel()
	result, err := h.Service.Fans(ctx)
	if err != nil {
		return readError(err)
	}
	encoded, err := json.Marshal(fansResult(result))
	if err != nil {
		return failure("unavailable")
	}
	return encoded
}
func (h Handler) PowerSupplies(ctx context.Context, data []byte, headers nats.Header) []byte {
	ctx, cancel, code := requestContext(ctx, data, headers, h.Timeout)
	if code != "" {
		return failure(code)
	}
	defer cancel()
	result, err := h.Service.PowerSupplies(ctx)
	if err != nil {
		return readError(err)
	}
	encoded, err := json.Marshal(powerSuppliesResult(result))
	if err != nil {
		return failure("unavailable")
	}
	return encoded
}
func readError(err error) []byte {
	code := "unavailable"
	if errors.Is(err, context.DeadlineExceeded) {
		code = "deadline_exceeded"
	} else if errors.Is(err, context.Canceled) {
		code = "canceled"
	}
	return failure(code)
}
func failure(code string) []byte { return []byte(`{"error":"` + code + `"}`) }
