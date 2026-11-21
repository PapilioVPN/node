// Package httpapi exposes native candidate validation through Fiber.
package httpapi

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	"github.com/gofiber/fiber/v3"
)

type Handler struct {
	Service *services.Service
	Timeout time.Duration
}

func (h Handler) Register(router fiber.Router) {
	router.Post("/v1/xray/runtime/instances/:id/config-validations", h.Validate)
}

func (h Handler) Validate(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	if request.Request().URI().QueryArgs().Len() != 1 || !request.Request().URI().QueryArgs().Has("format") {
		return failure(request, domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.Timeout)
	defer cancel()
	result, err := h.Service.Validate(ctx, request.Params("id"), domain.Candidate{Format: domain.Format(request.Query("format")), Content: request.Body()})
	if err != nil {
		return failure(request, err)
	}
	return request.JSON(struct {
		InstanceID string    `json:"instance_id"`
		Valid      bool      `json:"valid"`
		CheckedAt  time.Time `json:"checked_at"`
		DurationNS string    `json:"duration_ns"`
	}{result.InstanceID, result.Valid, result.CheckedAt, strconv.FormatInt(int64(result.Duration), 10)})
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
	case errors.Is(err, domain.ErrUnsupported), errors.Is(err, domain.ErrClosed):
		code, status = "failed_precondition", fiber.StatusConflict
	case errors.Is(err, domain.ErrTooLarge):
		code, status = "resource_exhausted", fiber.StatusRequestEntityTooLarge
	case errors.Is(err, domain.ErrCapacity):
		code, status = "resource_exhausted", fiber.StatusTooManyRequests
	}
	return request.Status(status).JSON(struct {
		Error string `json:"error"`
	}{code})
}
