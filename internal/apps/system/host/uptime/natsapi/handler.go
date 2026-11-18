// Package natsapi exposes host uptime through bounded Core NATS request/reply.
package natsapi

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/uptime/app/services"
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
	ctx, cancel := context.WithTimeout(ctx, handler.Timeout)
	defer cancel()
	if values, ok := headers["Papilio-Deadline"]; ok {
		if len(values) != 1 {
			return failure("invalid_argument")
		}
		deadline, err := time.Parse(time.RFC3339Nano, values[0])
		if err != nil {
			return failure("invalid_argument")
		}
		var cancelDeadline context.CancelFunc
		ctx, cancelDeadline = context.WithDeadline(ctx, deadline)
		defer cancelDeadline()
	}
	result, err := handler.Service.Read(ctx)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return failure("deadline_exceeded")
		}
		if errors.Is(err, context.Canceled) {
			return failure("canceled")
		}
		return failure("unavailable")
	}
	encoded, err := json.Marshal(response{
		UptimeNS:   strconv.FormatInt(int64(result.Uptime), 10),
		ObservedAt: result.ObservedAt, DurationNS: strconv.FormatInt(int64(result.Duration), 10),
	})
	if err != nil {
		return failure("unavailable")
	}
	return encoded
}

func failure(code string) []byte { return []byte(`{"error":"` + code + `"}`) }

type response struct {
	UptimeNS   string    `json:"uptime_ns"`
	ObservedAt time.Time `json:"observed_at"`
	DurationNS string    `json:"duration_ns"`
}
