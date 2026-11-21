// Package httpapi exposes native Xray runtime observations through Fiber.
package httpapi

import (
	"context"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/statistics/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/statistics/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/statistics/infra/jsonwire"
	"github.com/gofiber/fiber/v3"
)

type Handler struct {
	Service *services.Service
	Timeout time.Duration
}

func (h Handler) Register(router fiber.Router) {
	router.Get("/v1/xray/runtime/instances/:id/statistics", h.Read)
}
func (h Handler) Read(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	if len(request.Request().URI().QueryString()) != 0 {
		return failure(request, domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.Timeout)
	defer cancel()
	result, err := h.Service.Read(ctx, request.Params("id"))
	if err != nil {
		return failure(request, err)
	}
	data, err := jsonwire.Encode(result)
	if err != nil {
		return failure(request, err)
	}
	request.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	return request.Send(data)
}
func failure(request fiber.Ctx, err error) error {
	code, status := "unavailable", fiber.StatusServiceUnavailable
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		code, status = "deadline_exceeded", fiber.StatusGatewayTimeout
	case errors.Is(err, context.Canceled):
		code = "canceled"
	case errors.Is(err, domain.ErrInvalidArgument):
		code, status = "invalid_argument", fiber.StatusBadRequest
	case errors.Is(err, domain.ErrNotFound):
		code, status = "not_found", fiber.StatusNotFound
	case errors.Is(err, domain.ErrUnsupported), errors.Is(err, domain.ErrClosed), errors.Is(err, domain.ErrNotRunning):
		code, status = "failed_precondition", fiber.StatusConflict
	case errors.Is(err, domain.ErrExecutionChanged):
		code, status = "execution_changed", fiber.StatusConflict
	case errors.Is(err, domain.ErrCapacity):
		code, status = "resource_exhausted", fiber.StatusTooManyRequests
	}
	return request.Status(status).JSON(struct {
		Error string `json:"error"`
	}{code})
}
