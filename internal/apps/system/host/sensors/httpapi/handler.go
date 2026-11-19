// Package httpapi exposes physical sensors through authenticated Fiber routes.
package httpapi

import (
	"context"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/sensors/app/services"
	"github.com/gofiber/fiber/v3"
)

type Handler struct {
	Service services.Service
	Timeout time.Duration
}

func (h Handler) Register(r fiber.Router) {
	r.Get("/v1/system/host/sensors/temperatures", h.Temperatures)
	r.Get("/v1/system/host/sensors/fans", h.Fans)
	r.Get("/v1/system/host/sensors/power-supplies", h.PowerSupplies)
}
func (h Handler) Temperatures(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	if len(request.Body()) != 0 || len(request.Request().URI().QueryString()) != 0 {
		return request.Status(400).JSON(fiber.Map{"error": "invalid_argument"})
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.Timeout)
	defer cancel()
	result, err := h.Service.Temperatures(ctx)
	if err != nil {
		return readError(request, err)
	}
	return request.JSON(temperaturesResult(result))
}
func (h Handler) Fans(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	if len(request.Body()) != 0 || len(request.Request().URI().QueryString()) != 0 {
		return request.Status(400).JSON(fiber.Map{"error": "invalid_argument"})
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.Timeout)
	defer cancel()
	result, err := h.Service.Fans(ctx)
	if err != nil {
		return readError(request, err)
	}
	return request.JSON(fansResult(result))
}
func (h Handler) PowerSupplies(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	if len(request.Body()) != 0 || len(request.Request().URI().QueryString()) != 0 {
		return request.Status(400).JSON(fiber.Map{"error": "invalid_argument"})
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.Timeout)
	defer cancel()
	result, err := h.Service.PowerSupplies(ctx)
	if err != nil {
		return readError(request, err)
	}
	return request.JSON(powerSuppliesResult(result))
}
func readError(r fiber.Ctx, err error) error {
	code, status := "unavailable", 503
	if errors.Is(err, context.DeadlineExceeded) {
		code, status = "deadline_exceeded", 504
	} else if errors.Is(err, context.Canceled) {
		code = "canceled"
	}
	return r.Status(status).JSON(fiber.Map{"error": code})
}
