// Package natsapi adapts network interface observations to Core NATS requests.
package natsapi

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/network/interfaces/app/services"
	"github.com/nats-io/nats.go"
)

type Handler struct {
	Service services.Service
	Timeout time.Duration
}

func (handler Handler) List(ctx context.Context, data []byte, headers nats.Header) []byte {
	if len(data) > 1024 {
		return failure("resource_exhausted")
	}
	var input map[string]json.RawMessage
	if err := json.Unmarshal(data, &input); err != nil || input == nil || len(input) != 0 {
		return failure("invalid_argument")
	}
	ctx, cancel, code := requestContext(ctx, headers, handler.Timeout)
	if code != "" {
		return failure(code)
	}
	defer cancel()
	result, err := handler.Service.List(ctx)
	if err != nil {
		return readError(err)
	}
	encoded, err := json.Marshal(listResult(result))
	if err != nil {
		return failure("unavailable")
	}
	return encoded
}

func (handler Handler) ReadCounters(ctx context.Context, data []byte, headers nats.Header) []byte {
	if len(data) > 1024 {
		return failure("resource_exhausted")
	}
	var input map[string]json.RawMessage
	if err := json.Unmarshal(data, &input); err != nil || input == nil || len(input) != 0 {
		return failure("invalid_argument")
	}
	ctx, cancel, code := requestContext(ctx, headers, handler.Timeout)
	if code != "" {
		return failure(code)
	}
	defer cancel()
	result, err := handler.Service.ReadCounters(ctx)
	if err != nil {
		return readError(err)
	}
	encoded, err := json.Marshal(counterResult(result))
	if err != nil {
		return failure("unavailable")
	}
	return encoded
}

func readError(err error) []byte {
	if errors.Is(err, context.DeadlineExceeded) {
		return failure("deadline_exceeded")
	}
	if errors.Is(err, context.Canceled) {
		return failure("canceled")
	}
	return failure("unavailable")
}
func failure(code string) []byte { return []byte(`{"error":"` + code + `"}`) }
