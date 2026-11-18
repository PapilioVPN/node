package natsapi

import (
	"context"
	"encoding/json"
	"time"

	"github.com/nats-io/nats.go"
)

func (handler Handler) ReadTimes(ctx context.Context, data []byte, headers nats.Header) []byte {
	if len(data) > 1024 {
		return failure("resource_exhausted")
	}
	var input map[string]json.RawMessage
	if err := json.Unmarshal(data, &input); err != nil || input == nil || len(input) > 1 {
		return failure("invalid_argument")
	}
	perCPU := false
	if len(input) != 0 {
		raw, ok := input["per_cpu"]
		var selection *bool
		if !ok {
			return failure("invalid_argument")
		}
		if err := json.Unmarshal(raw, &selection); err != nil || selection == nil {
			return failure("invalid_argument")
		}
		perCPU = *selection
	}
	ctx, cancel, code := requestContext(ctx, headers, handler.Timeout)
	if code != "" {
		return failure(code)
	}
	defer cancel()
	result, err := handler.Service.ReadTimes(ctx, perCPU)
	if err != nil {
		return readError(err)
	}
	rows := make([]ticksResponse, 0, len(result.Times.Counters))
	for _, row := range result.Times.Counters {
		rows = append(rows, ticksResponse{CPU: row.CPU, User: row.User, Nice: row.Nice, System: row.System, Idle: row.Idle, IOWait: row.IOWait, IRQ: row.IRQ, SoftIRQ: row.SoftIRQ, Steal: row.Steal, Guest: row.Guest, GuestNice: row.GuestNice})
	}
	encoded, err := json.Marshal(timesResponse{PerCPU: result.Times.PerCPU, TicksPerSecond: result.Times.TicksPerSecond, Counters: rows, ObservedAt: result.ObservedAt, DurationNS: int64(result.Duration)})
	if err != nil {
		return failure("unavailable")
	}
	return encoded
}

type ticksResponse struct {
	CPU       string `json:"cpu"`
	User      uint64 `json:"user_ticks,string"`
	Nice      uint64 `json:"nice_ticks,string"`
	System    uint64 `json:"system_ticks,string"`
	Idle      uint64 `json:"idle_ticks,string"`
	IOWait    uint64 `json:"iowait_ticks,string"`
	IRQ       uint64 `json:"irq_ticks,string"`
	SoftIRQ   uint64 `json:"softirq_ticks,string"`
	Steal     uint64 `json:"steal_ticks,string"`
	Guest     uint64 `json:"guest_ticks,string"`
	GuestNice uint64 `json:"guest_nice_ticks,string"`
}

type timesResponse struct {
	PerCPU         bool            `json:"per_cpu"`
	TicksPerSecond uint64          `json:"ticks_per_second,string"`
	Counters       []ticksResponse `json:"counters"`
	ObservedAt     time.Time       `json:"observed_at"`
	DurationNS     int64           `json:"duration_ns,string"`
}
