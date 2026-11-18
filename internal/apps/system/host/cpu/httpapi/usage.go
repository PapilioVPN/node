package httpapi

import (
	"context"
	"net/url"
	"strconv"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/domain"
	"github.com/gofiber/fiber/v3"
)

func (handler Handler) SampleUsage(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	if len(request.Body()) != 0 {
		return request.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid_argument"})
	}
	query, err := url.ParseQuery(string(request.Request().URI().QueryString()))
	if err != nil {
		return request.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid_argument"})
	}
	for key, values := range query {
		if (key != "per_cpu" && key != "interval_ns") || len(values) != 1 {
			return request.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid_argument"})
		}
	}
	input := domain.UsageRequest{Interval: domain.DefaultSampleInterval}
	if values, ok := query["per_cpu"]; ok {
		input.PerCPU, err = strconv.ParseBool(values[0])
		if err != nil {
			return request.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid_argument"})
		}
	}
	if values, ok := query["interval_ns"]; ok {
		interval, err := strconv.ParseInt(values[0], 10, 64)
		if err != nil {
			return request.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid_argument"})
		}
		input.Interval = time.Duration(interval)
	}
	ctx, cancel := context.WithTimeout(request.Context(), handler.Timeout)
	defer cancel()
	result, err := handler.Service.SampleUsage(ctx, input)
	if err != nil {
		return readError(request, err)
	}
	rows := make([]usageResponse, 0, len(result.CPUs))
	for _, row := range result.CPUs {
		rows = append(rows, usageResponse{CPU: row.CPU, State: string(row.State), Percent: row.Percent, BusyTicks: row.BusyTicks, TotalTicks: row.TotalTicks})
	}
	return request.JSON(usageObservationResponse{PerCPU: result.PerCPU, TicksPerSecond: result.TicksPerSecond, CPUs: rows, RequestedIntervalNS: int64(result.RequestedInterval), StartedAt: result.StartedAt, SampleIntervalNS: int64(result.SampleInterval), ObservedAt: result.ObservedAt, DurationNS: int64(result.Duration)})
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
