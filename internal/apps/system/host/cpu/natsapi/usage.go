package natsapi

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/domain"
	"github.com/nats-io/nats.go"
)

func (handler Handler) SampleUsage(ctx context.Context, data []byte, headers nats.Header) []byte {
	if len(data) > 1024 {
		return failure("resource_exhausted")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return failure("invalid_argument")
	}
	for key := range fields {
		if key != "per_cpu" && key != "interval_ns" {
			return failure("invalid_argument")
		}
	}
	input := domain.UsageRequest{Interval: domain.DefaultSampleInterval}
	if raw, ok := fields["per_cpu"]; ok {
		var value *bool
		if err := json.Unmarshal(raw, &value); err != nil || value == nil {
			return failure("invalid_argument")
		}
		input.PerCPU = *value
	}
	if raw, ok := fields["interval_ns"]; ok {
		var value *string
		if err := json.Unmarshal(raw, &value); err != nil || value == nil {
			return failure("invalid_argument")
		}
		interval, err := strconv.ParseInt(*value, 10, 64)
		if err != nil {
			return failure("invalid_argument")
		}
		input.Interval = time.Duration(interval)
	}
	ctx, cancel, code := requestContext(ctx, headers, handler.Timeout)
	if code != "" {
		return failure(code)
	}
	defer cancel()
	result, err := handler.Service.SampleUsage(ctx, input)
	if err != nil {
		return readError(err)
	}
	rows := make([]usageResponse, 0, len(result.CPUs))
	for _, row := range result.CPUs {
		rows = append(rows, usageResponse{CPU: row.CPU, State: string(row.State), Percent: row.Percent, BusyTicks: row.BusyTicks, TotalTicks: row.TotalTicks})
	}
	encoded, err := json.Marshal(usageObservationResponse{PerCPU: result.PerCPU, TicksPerSecond: result.TicksPerSecond, CPUs: rows, RequestedIntervalNS: int64(result.RequestedInterval), StartedAt: result.StartedAt, SampleIntervalNS: int64(result.SampleInterval), ObservedAt: result.ObservedAt, DurationNS: int64(result.Duration)})
	if err != nil {
		return failure("unavailable")
	}
	return encoded
}

type usageResponse struct {
	CPU        string   `json:"cpu"`
	State      string   `json:"state"`
	Percent    *float64 `json:"percent"`
	BusyTicks  *uint64  `json:"busy_ticks,string"`
	TotalTicks *uint64  `json:"total_ticks,string"`
}

type usageObservationResponse struct {
	PerCPU              bool            `json:"per_cpu"`
	TicksPerSecond      uint64          `json:"ticks_per_second,string"`
	CPUs                []usageResponse `json:"cpus"`
	RequestedIntervalNS int64           `json:"requested_interval_ns,string"`
	StartedAt           time.Time       `json:"started_at"`
	SampleIntervalNS    int64           `json:"sample_interval_ns,string"`
	ObservedAt          time.Time       `json:"observed_at"`
	DurationNS          int64           `json:"duration_ns,string"`
}
