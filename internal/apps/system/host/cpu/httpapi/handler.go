// Package httpapi exposes authenticated host processor observations through Fiber.
package httpapi

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/domain"
	"github.com/gofiber/fiber/v3"
)

type Handler struct {
	Service services.Service
	Timeout time.Duration
}

func (handler Handler) Register(router fiber.Router) {
	router.Get("/v1/system/host/load", handler.Read)
	router.Get("/v1/system/host/cpu/times", handler.ReadTimes)
	router.Get("/v1/system/host/cpu/usage", handler.SampleUsage)
	router.Get("/v1/system/host/cpu/info", handler.ReadInfo)
	router.Get("/v1/system/host/cpu/frequency", handler.ReadFrequency)
	router.Get("/v1/system/host/cpu/activity", handler.ReadActivity)
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
		OneMinute: result.Averages.OneMinute, FiveMinutes: result.Averages.FiveMinutes, FifteenMinutes: result.Averages.FifteenMinutes,
		ObservedAt: result.ObservedAt, DurationNS: strconv.FormatInt(int64(result.Duration), 10),
	})
}

func readError(request fiber.Ctx, err error) error {
	if errors.Is(err, domain.ErrInvalidSampleInterval) {
		return request.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid_argument"})
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return request.Status(fiber.StatusGatewayTimeout).JSON(fiber.Map{"error": "deadline_exceeded"})
	}
	if errors.Is(err, context.Canceled) {
		return request.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "canceled"})
	}
	return request.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "unavailable"})
}

type response struct {
	OneMinute      float64   `json:"one_minute"`
	FiveMinutes    float64   `json:"five_minutes"`
	FifteenMinutes float64   `json:"fifteen_minutes"`
	ObservedAt     time.Time `json:"observed_at"`
	DurationNS     string    `json:"duration_ns"`
}
