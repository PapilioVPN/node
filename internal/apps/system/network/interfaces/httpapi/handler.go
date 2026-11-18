// Package httpapi adapts network interface observations to Fiber.
package httpapi

import (
	"context"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/network/interfaces/app/services"
	"github.com/gofiber/fiber/v3"
)

type Handler struct {
	Service services.Service
	Timeout time.Duration
}

func (handler Handler) Register(router fiber.Router) {
	router.Get("/v1/system/network/interfaces", handler.List)
	router.Get("/v1/system/network/interfaces/counters", handler.ReadCounters)
}

func (handler Handler) List(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	if len(request.Body()) != 0 || len(request.Request().URI().QueryString()) != 0 {
		return request.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid_argument"})
	}
	ctx, cancel := context.WithTimeout(request.Context(), handler.Timeout)
	defer cancel()
	result, err := handler.Service.List(ctx)
	if err != nil {
		return readError(request, err)
	}
	return request.JSON(listResult(result))
}

func (handler Handler) ReadCounters(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	if len(request.Body()) != 0 || len(request.Request().URI().QueryString()) != 0 {
		return request.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid_argument"})
	}
	ctx, cancel := context.WithTimeout(request.Context(), handler.Timeout)
	defer cancel()
	result, err := handler.Service.ReadCounters(ctx)
	if err != nil {
		return readError(request, err)
	}
	return request.JSON(counterResult(result))
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
