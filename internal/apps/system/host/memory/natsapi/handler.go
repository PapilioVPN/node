// Package natsapi exposes host memory through bounded Core NATS request/reply.
package natsapi

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/memory/app/services"
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
		TotalBytes:     result.Memory.TotalBytes,
		AvailableBytes: result.Memory.AvailableBytes,
		UsedBytes:      result.Memory.UsedBytes,
		FreeBytes:      result.Memory.FreeBytes,
		ActiveBytes:    result.Memory.ActiveBytes,
		InactiveBytes:  result.Memory.InactiveBytes,
		BuffersBytes:   result.Memory.BuffersBytes,
		CachedBytes:    result.Memory.CachedBytes,
		SharedBytes:    result.Memory.SharedBytes,
		SlabBytes:      result.Memory.SlabBytes,
		UsedPercent:    result.Memory.UsedPercent,
		ObservedAt:     result.ObservedAt, DurationNS: int64(result.Duration),
	})
	if err != nil {
		return failure("unavailable")
	}
	return encoded
}

func failure(code string) []byte { return []byte(`{"error":"` + code + `"}`) }

type response struct {
	TotalBytes     uint64    `json:"total_bytes,string"`
	AvailableBytes uint64    `json:"available_bytes,string"`
	UsedBytes      uint64    `json:"used_bytes,string"`
	FreeBytes      uint64    `json:"free_bytes,string"`
	ActiveBytes    uint64    `json:"active_bytes,string"`
	InactiveBytes  uint64    `json:"inactive_bytes,string"`
	BuffersBytes   uint64    `json:"buffers_bytes,string"`
	CachedBytes    uint64    `json:"cached_bytes,string"`
	SharedBytes    uint64    `json:"shared_bytes,string"`
	SlabBytes      uint64    `json:"slab_bytes,string"`
	UsedPercent    float64   `json:"used_percent"`
	ObservedAt     time.Time `json:"observed_at"`
	DurationNS     int64     `json:"duration_ns,string"`
}
