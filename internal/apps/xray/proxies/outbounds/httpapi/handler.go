// Package httpapi exposes bounded outbound observations through Fiber.
package httpapi

import (
	"context"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/infra/jsonwire"
	"github.com/gofiber/fiber/v3"
)

type Handler struct {
	Service      *services.Service
	Sources      *services.Sources
	Replacements *services.Replacements
	Timeout      time.Duration
}

func (h Handler) Register(router fiber.Router) {
	router.Get("/v1/xray/proxies/instances/:id/outbounds", h.Records)
	router.Post("/v1/xray/proxies/instances/:id/outbounds", h.Add)
	router.Delete("/v1/xray/proxies/instances/:id/outbounds", h.Remove)
	router.Get("/v1/xray/proxies/instances/:id/outbounds/control-status", h.Status)
	router.Post("/v1/xray/proxies/instances/:id/outbounds/replacement-plans", h.PlanReplacement)
	router.Post("/v1/xray/proxies/instances/:id/outbounds/replacements", h.ApplyReplacement)
	router.Get("/v1/xray/proxies/instances/:id/outbounds/replacements/:plan", h.ReplacementResult)
}
func (h Handler) Records(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	paging, err := pageRequest(request)
	if err != nil {
		return failure(request, err)
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.Timeout)
	defer cancel()
	result, err := h.Service.List(ctx, request.Params("id"), paging)
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
func errorStatus(err error) (string, int) {
	code, status := "unavailable", fiber.StatusServiceUnavailable
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		code, status = "deadline_exceeded", fiber.StatusGatewayTimeout
	case errors.Is(err, context.Canceled):
		code = "canceled"
	case errors.Is(err, domain.ErrInvalidArgument):
		code, status = "invalid_argument", fiber.StatusBadRequest
	case errors.Is(err, domain.ErrContinuation):
		code, status = "continuation_expired", fiber.StatusConflict
	case errors.Is(err, domain.ErrPlanExpired):
		code, status = "plan_expired", fiber.StatusConflict
	case errors.Is(err, domain.ErrStalePlan):
		code, status = "stale_plan", fiber.StatusConflict
	case errors.Is(err, domain.ErrOutboundNotFound):
		code, status = "outbound_not_found", fiber.StatusNotFound
	case errors.Is(err, domain.ErrNotFound):
		code, status = "not_found", fiber.StatusNotFound
	case errors.Is(err, domain.ErrUnsupported), errors.Is(err, domain.ErrNotRunning):
		code, status = "failed_precondition", fiber.StatusConflict
	case errors.Is(err, domain.ErrUncertain):
		code, status = "restart_required", fiber.StatusConflict
	case errors.Is(err, domain.ErrExecutionChanged):
		code, status = "execution_changed", fiber.StatusConflict
	case errors.Is(err, domain.ErrCapacity):
		code, status = "resource_exhausted", fiber.StatusTooManyRequests
	}
	return code, status
}
func failure(request fiber.Ctx, err error) error {
	code, status := errorStatus(err)
	return request.Status(status).JSON(struct {
		Error string `json:"error"`
	}{code})
}
