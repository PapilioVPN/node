// Package httpapi exposes configured instance executions through Fiber.
package httpapi

import (
	"context"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	"github.com/gofiber/fiber/v3"
)

type Handler struct {
	Service *services.Service
	Timeout time.Duration
}

func (h Handler) Register(router fiber.Router) {
	const root = "/v1/core/engines/instances/:id"
	router.Get(root, h.Status)
	router.Put(root+"/execution", h.Start)
	router.Delete(root+"/execution", h.Stop)
	router.Post(root+"/executions", h.Restart)
	router.Get(root+"/readiness", h.Check)
}
func (h Handler) Status(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	if !empty(request) {
		return fail(request, domain.ErrInvalidArgument, nil)
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.Timeout)
	defer cancel()
	result, err := h.Service.Status(ctx, request.Params("id"))
	if err != nil {
		return fail(request, err, nil)
	}
	return request.JSON(statusResult(result))
}
func (h Handler) Start(request fiber.Ctx) error   { return h.control(request, h.Service.Start) }
func (h Handler) Stop(request fiber.Ctx) error    { return h.control(request, h.Service.Stop) }
func (h Handler) Restart(request fiber.Ctx) error { return h.control(request, h.Service.Restart) }
func (h Handler) control(request fiber.Ctx, operation func(context.Context, string) (domain.Control, error)) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	if !empty(request) {
		return fail(request, domain.ErrInvalidArgument, &domain.Control{Effect: domain.NotApplied})
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.Timeout)
	defer cancel()
	result, err := operation(ctx, request.Params("id"))
	if err != nil {
		return fail(request, err, &result)
	}
	return request.JSON(controlResult(result))
}
func (h Handler) Check(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	if !empty(request) {
		return fail(request, domain.ErrInvalidArgument, nil)
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.Timeout)
	defer cancel()
	result, err := h.Service.Check(ctx, request.Params("id"))
	if err != nil {
		return fail(request, err, nil)
	}
	return request.JSON(readinessResult(result))
}
func empty(request fiber.Ctx) bool {
	return len(request.Body()) == 0 && len(request.Request().URI().QueryString()) == 0
}
func fail(request fiber.Ctx, err error, result *domain.Control) error {
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
	case errors.Is(err, domain.ErrBusy):
		code, status = "resource_exhausted", fiber.StatusTooManyRequests
	case errors.Is(err, domain.ErrClosed), errors.Is(err, domain.ErrUnsupported):
		code, status = "failed_precondition", fiber.StatusConflict
	}
	response := errorResponse{Error: code}
	if result != nil {
		value := controlResult(*result)
		response.Control = &value
	}
	return request.Status(status).JSON(response)
}
