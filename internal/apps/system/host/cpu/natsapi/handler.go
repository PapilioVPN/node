// Package natsapi exposes host processor observations through bounded Core NATS request/reply.
package natsapi

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/domain"
	"github.com/nats-io/nats.go"
)

type Handler struct {
	Service services.Service
	Timeout time.Duration
}

// Read accepts an empty JSON object. An optional absolute deadline only shortens
// the operation timeout; Core NATS does not propagate caller disconnection.
func (handler Handler) Read(ctx context.Context, data []byte, headers nats.Header) []byte {
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
	result, err := handler.Service.Read(ctx)
	if err != nil {
		return readError(err)
	}
	encoded, err := json.Marshal(response{
		OneMinute: result.Averages.OneMinute, FiveMinutes: result.Averages.FiveMinutes, FifteenMinutes: result.Averages.FifteenMinutes,
		ObservedAt: result.ObservedAt, DurationNS: strconv.FormatInt(int64(result.Duration), 10),
	})
	if err != nil {
		return failure("unavailable")
	}
	return encoded
}

func failure(code string) []byte { return []byte(`{"error":"` + code + `"}`) }

type response struct {
	OneMinute      float64   `json:"one_minute"`
	FiveMinutes    float64   `json:"five_minutes"`
	FifteenMinutes float64   `json:"fifteen_minutes"`
	ObservedAt     time.Time `json:"observed_at"`
	DurationNS     string    `json:"duration_ns"`
}

func requestContext(ctx context.Context, headers nats.Header, timeout time.Duration) (context.Context, context.CancelFunc, string) {
	deadline := time.Now().Add(timeout)
	if values, ok := headers["Papilio-Deadline"]; ok {
		if len(values) != 1 {
			return nil, nil, "invalid_argument"
		}
		requested, err := time.Parse(time.RFC3339Nano, values[0])
		if err != nil {
			return nil, nil, "invalid_argument"
		}
		if requested.Before(deadline) {
			deadline = requested
		}
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	return ctx, cancel, ""
}

func readError(err error) []byte {
	if errors.Is(err, domain.ErrInvalidSampleInterval) {
		return failure("invalid_argument")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return failure("deadline_exceeded")
	}
	if errors.Is(err, context.Canceled) {
		return failure("canceled")
	}
	return failure("unavailable")
}
