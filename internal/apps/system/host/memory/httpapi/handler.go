// Package httpapi exposes the authenticated host memory read through Fiber.
package httpapi

import (
	"context"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/memory/app/services"
	"github.com/gofiber/fiber/v3"
)

type Handler struct {
	Service services.Service
	Timeout time.Duration
}

func (handler Handler) Register(router fiber.Router) {
	router.Get("/v1/system/host/memory", handler.Read)
	router.Get("/v1/system/host/swap", handler.ReadSwap)
}

func (handler Handler) Read(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	if len(request.Body()) != 0 || len(request.Request().URI().QueryString()) != 0 {
		return request.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid_argument"})
	}
	// Fiber does not propagate ordinary client disconnects. Bound this synchronous
	// read explicitly; do not retain the pooled request context in application code.
	ctx, cancel := context.WithTimeout(request.Context(), handler.Timeout)
	defer cancel()
	result, err := handler.Service.Read(ctx)
	if err != nil {
		return readError(request, err)
	}
	return request.JSON(response{
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
}

func (handler Handler) ReadSwap(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	if len(request.Body()) != 0 || len(request.Request().URI().QueryString()) != 0 {
		return request.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid_argument"})
	}
	ctx, cancel := context.WithTimeout(request.Context(), handler.Timeout)
	defer cancel()
	result, err := handler.Service.ReadSwap(ctx)
	if err != nil {
		return readError(request, err)
	}
	return request.JSON(swapResponse{
		TotalBytes: result.Swap.TotalBytes, UsedBytes: result.Swap.UsedBytes, FreeBytes: result.Swap.FreeBytes,
		SwappedInBytes: result.Swap.SwappedInBytes, SwappedOutBytes: result.Swap.SwappedOutBytes, UsedPercent: result.Swap.UsedPercent,
		ObservedAt: result.ObservedAt, DurationNS: int64(result.Duration),
	})
}

func readError(request fiber.Ctx, err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return request.Status(fiber.StatusGatewayTimeout).JSON(fiber.Map{"error": "deadline_exceeded"})
	}
	if errors.Is(err, context.Canceled) {
		return request.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "canceled"})
	}
	return request.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "unavailable"})
}

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

type swapResponse struct {
	TotalBytes      uint64    `json:"total_bytes,string"`
	UsedBytes       uint64    `json:"used_bytes,string"`
	FreeBytes       uint64    `json:"free_bytes,string"`
	SwappedInBytes  uint64    `json:"swapped_in_bytes,string"`
	SwappedOutBytes uint64    `json:"swapped_out_bytes,string"`
	UsedPercent     float64   `json:"used_percent"`
	ObservedAt      time.Time `json:"observed_at"`
	DurationNS      int64     `json:"duration_ns,string"`
}
