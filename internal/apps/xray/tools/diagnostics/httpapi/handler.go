// Package httpapi exposes shareable read-only instance diagnostic reports.
package httpapi

import (
	"context"
	"strconv"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/tools/diagnostics/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/diagnostics/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/diagnostics/infra/jsonwire"
	"github.com/gofiber/fiber/v3"
)

type Handler struct {
	Service *services.Service
	Timeout time.Duration
}

func (h Handler) Register(r fiber.Router) {
	r.Get("/v1/xray/tools/instances/:id/diagnostics", h.Report)
}
func (h Handler) Report(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	if len(request.Body()) > 0 || len(request.Request().URI().QueryString()) > jsonwire.MaxRequestBytes {
		return fail(request, domain.ErrInvalidArgument)
	}
	input := domain.Request{InstanceID: request.Params("id")}
	seen, valid := false, true
	request.Request().URI().QueryArgs().VisitAll(func(key, value []byte) {
		if string(key) != "max_age_seconds" || seen {
			valid = false
			return
		}
		seen = true
		n, err := strconv.ParseUint(string(value), 10, 32)
		if err != nil {
			valid = false
			return
		}
		input.MaxAgeSeconds = uint32(n)
	})
	if !valid {
		return fail(request, domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.Timeout)
	defer cancel()
	report, err := h.Service.Report(ctx, input)
	if err != nil {
		return fail(request, err)
	}
	data, err := jsonwire.Encode(report)
	if err != nil {
		return fail(request, err)
	}
	request.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	return request.Send(data)
}
func fail(request fiber.Ctx, err error) error {
	code := jsonwire.ErrorCode(err)
	status := fiber.StatusServiceUnavailable
	switch code {
	case "invalid_argument":
		status = fiber.StatusBadRequest
	case "not_found":
		status = fiber.StatusNotFound
	case "resource_exhausted":
		status = fiber.StatusTooManyRequests
	case "deadline_exceeded":
		status = fiber.StatusGatewayTimeout
	}
	request.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	return request.Status(status).Send(jsonwire.Failure(err))
}
