// Package natsapi exposes bounded Node runtime observations through Core NATS.
package natsapi

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/node/runtime/app/services"
	"github.com/nats-io/nats.go"
)

type Handler struct {
	Service services.Service
	Timeout time.Duration
}

func (handler Handler) Info(ctx context.Context, data []byte, headers nats.Header) []byte {
	ctx, cancel, code := requestContext(ctx, data, headers, handler.Timeout)
	if code != "" {
		return failure(code)
	}
	defer cancel()
	result, err := handler.Service.Info(ctx)
	if err != nil {
		return readError(err)
	}
	encoded, err := json.Marshal(infoResult(result))
	if err != nil {
		return failure("unavailable")
	}
	return encoded
}
func (handler Handler) Resources(ctx context.Context, data []byte, headers nats.Header) []byte {
	ctx, cancel, code := requestContext(ctx, data, headers, handler.Timeout)
	if code != "" {
		return failure(code)
	}
	defer cancel()
	result, err := handler.Service.Resources(ctx)
	if err != nil {
		return readError(err)
	}
	encoded, err := json.Marshal(resourcesResult(result))
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

func (handler Handler) Diagnostics(ctx context.Context, data []byte, headers nats.Header) []byte {
	ctx, cancel, code := requestContext(ctx, data, headers, handler.Timeout)
	if code != "" {
		return failure(code)
	}
	defer cancel()
	result, err := handler.Service.Diagnostics(ctx)
	if err != nil {
		return readError(err)
	}
	encoded, err := json.Marshal(diagnosticsResult(result))
	if err != nil {
		return failure("unavailable")
	}
	return encoded
}
