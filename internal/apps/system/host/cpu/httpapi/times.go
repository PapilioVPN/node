package httpapi

import (
	"context"
	"net/url"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v3"
)

func (handler Handler) ReadTimes(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	if len(request.Body()) != 0 {
		return request.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid_argument"})
	}
	query, err := url.ParseQuery(string(request.Request().URI().QueryString()))
	if err != nil || len(query) > 1 {
		return request.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid_argument"})
	}
	perCPU := false
	if len(query) != 0 {
		values, ok := query["per_cpu"]
		if !ok || len(values) != 1 {
			return request.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid_argument"})
		}
		perCPU, err = strconv.ParseBool(values[0])
		if err != nil {
			return request.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid_argument"})
		}
	}
	ctx, cancel := context.WithTimeout(request.Context(), handler.Timeout)
	defer cancel()
	result, err := handler.Service.ReadTimes(ctx, perCPU)
	if err != nil {
		return readError(request, err)
	}
	rows := make([]ticksResponse, 0, len(result.Times.Counters))
	for _, row := range result.Times.Counters {
		rows = append(rows, ticksResponse{CPU: row.CPU, User: row.User, Nice: row.Nice, System: row.System, Idle: row.Idle, IOWait: row.IOWait, IRQ: row.IRQ, SoftIRQ: row.SoftIRQ, Steal: row.Steal, Guest: row.Guest, GuestNice: row.GuestNice})
	}
	return request.JSON(timesResponse{PerCPU: result.Times.PerCPU, TicksPerSecond: result.Times.TicksPerSecond, Counters: rows, ObservedAt: result.ObservedAt, DurationNS: int64(result.Duration)})
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
