// Package httpapi exposes exact byte observations through Fiber.
package httpapi

import (
	"context"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/infra/jsonwire"
	"github.com/gofiber/fiber/v3"
)

type Handler struct {
	Service *services.Service
	Timeout time.Duration
}

func (h Handler) Register(router fiber.Router) {
	router.Get("/v1/xray/observability/instances/:id/traffic/counter", h.Read)
	router.Get("/v1/xray/observability/instances/:id/traffic/counters", h.List)
	router.Get("/v1/xray/observability/instances/:id/traffic/pair", h.Pair)
	router.Post("/v1/xray/observability/instances/:id/traffic/counter-resets", h.ResetCounter)
	router.Post("/v1/xray/observability/instances/:id/traffic/pair-resets", h.ResetPair)
	router.Post("/v1/xray/observability/instances/:id/traffic/scope-resets", h.ResetScope)
	router.Get("/v1/xray/observability/instances/:id/traffic/reset-status", h.ResetStatus)
}
func (h Handler) Read(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	if len(request.Body()) != 0 || len(request.Request().URI().QueryString()) > jsonwire.MaxRequestBytes {
		return failure(request, domain.ErrInvalidArgument)
	}
	fields := make(map[string]string, 3)
	valid := true
	request.Request().URI().QueryArgs().VisitAll(func(key, value []byte) {
		name := string(key)
		switch name {
		case "scope", "name", "direction":
		default:
			valid = false
		}
		if _, exists := fields[name]; exists {
			valid = false
		}
		fields[name] = string(value)
	})
	if !valid || len(fields) != 3 {
		return failure(request, domain.ErrInvalidArgument)
	}
	selection := domain.Selection{Scope: domain.Scope(fields["scope"]), Name: fields["name"], Direction: domain.Direction(fields["direction"])}
	ctx, cancel := context.WithTimeout(request.Context(), h.Timeout)
	defer cancel()
	result, err := h.Service.Read(ctx, request.Params("id"), selection)
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
	case errors.Is(err, domain.ErrConflict):
		code, status = "revision_conflict", fiber.StatusConflict
	case errors.Is(err, domain.ErrContinuation):
		code, status = "continuation_expired", fiber.StatusConflict
	case errors.Is(err, domain.ErrRestartRequired):
		code, status = "restart_required", fiber.StatusConflict
	case errors.Is(err, domain.ErrExecutionChanged):
		code, status = "execution_changed", fiber.StatusConflict
	case errors.Is(err, domain.ErrCounterNotFound):
		code, status = "counter_not_found", fiber.StatusNotFound
	case errors.Is(err, domain.ErrNotFound):
		code, status = "not_found", fiber.StatusNotFound
	case errors.Is(err, domain.ErrUnsupported), errors.Is(err, domain.ErrClosed), errors.Is(err, domain.ErrNotRunning):
		code, status = "failed_precondition", fiber.StatusConflict
	case errors.Is(err, domain.ErrCapacity):
		code, status = "resource_exhausted", fiber.StatusTooManyRequests
	}
	return request.Status(status).JSON(struct {
		Error string `json:"error"`
	}{code})
}
