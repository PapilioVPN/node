// Package httpapi exposes Node process information through authenticated Fiber.
package httpapi

import (
	"context"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/node/runtime/app/services"
	"github.com/gofiber/fiber/v3"
)

type Handler struct {
	Service services.Service
	Timeout time.Duration
}

func (handler Handler) Register(router fiber.Router) {
	router.Get("/v1/system/node/runtime", handler.Info)
	router.Get("/v1/system/node/runtime/diagnostics", handler.Diagnostics)
	router.Get("/v1/system/node/runtime/resources", handler.Resources)
}
func (handler Handler) Info(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	if len(request.Body()) != 0 || len(request.Request().URI().QueryString()) != 0 {
		return request.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid_argument"})
	}
	ctx, cancel := context.WithTimeout(request.Context(), handler.Timeout)
	defer cancel()
	result, err := handler.Service.Info(ctx)
	if err != nil {
		return readError(request, err)
	}
	return request.JSON(infoResult(result))
}
func (handler Handler) Resources(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	if len(request.Body()) != 0 || len(request.Request().URI().QueryString()) != 0 {
		return request.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid_argument"})
	}
	ctx, cancel := context.WithTimeout(request.Context(), handler.Timeout)
	defer cancel()
	result, err := handler.Service.Resources(ctx)
	if err != nil {
		return readError(request, err)
	}
	return request.JSON(resourcesResult(result))
}
func readError(request fiber.Ctx, err error) error {
	code, status := "unavailable", fiber.StatusServiceUnavailable
	if errors.Is(err, context.DeadlineExceeded) {
		code, status = "deadline_exceeded", fiber.StatusGatewayTimeout
	} else if errors.Is(err, context.Canceled) {
		code = "canceled"
	}
	return request.Status(status).JSON(fiber.Map{"error": code})
}

func (handler Handler) Diagnostics(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	if len(request.Body()) != 0 || len(request.Request().URI().QueryString()) != 0 {
		return request.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid_argument"})
	}
	ctx, cancel := context.WithTimeout(request.Context(), handler.Timeout)
	defer cancel()
	result, err := handler.Service.Diagnostics(ctx)
	if err != nil {
		return readError(request, err)
	}
	return request.JSON(diagnosticsResult(result))
}
