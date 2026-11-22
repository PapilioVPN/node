// Package httpapi exposes outbound health observations through Fiber.
package httpapi

import (
	"context"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/outboundhealth/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/outboundhealth/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/outboundhealth/infra/jsonwire"
	"github.com/gofiber/fiber/v3"
)

type Handler struct {
	Service *services.Service
	Timeout time.Duration
}

func (h Handler) Register(router fiber.Router) {
	router.Get("/v1/xray/observability/instances/:id/outbound-health/status", h.Get)
	router.Get("/v1/xray/observability/instances/:id/outbound-health/statuses", h.List)
}
func decode(request fiber.Ctx, collection bool) (jsonwire.Request, error) {
	if len(request.Body()) != 0 || len(request.Request().URI().QueryString()) > jsonwire.MaxRequestBytes {
		return jsonwire.Request{}, domain.ErrInvalidArgument
	}

	input := jsonwire.Request{InstanceID: request.Params("id")}
	seen := map[string]bool{}
	valid := true
	request.Request().URI().QueryArgs().VisitAll(func(key, value []byte) {
		if !utf8.Valid(key) || !utf8.Valid(value) {
			valid = false
			return
		}
		name := string(key)
		if seen[name] {
			valid = false
			return
		}
		seen[name] = true
		switch name {
		case "outbound_tag":
			if collection {
				valid = false
				return
			}
			tag := string(value)
			input.Tag = &tag
		case "page_token":
			if !collection {
				valid = false
				return
			}
			input.Page.PageToken = string(value)
		case "page_size", "max_sample_age_seconds":
			if !collection && name == "page_size" {
				valid = false
				return
			}
			n, err := strconv.ParseUint(string(value), 10, 32)
			if err != nil {
				valid = false
				return
			}
			if name == "page_size" {
				input.Page.PageSize = uint32(n)
			} else {
				input.Page.MaxSampleAgeSeconds = uint32(n)
			}
		default:
			valid = false
		}
	})
	if !valid || (!collection && input.Tag == nil) {
		return jsonwire.Request{}, domain.ErrInvalidArgument
	}
	return input, nil
}
func (h Handler) Get(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	input, err := decode(request, false)
	if err != nil {
		return failure(request, err)
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.Timeout)
	defer cancel()
	result, err := h.Service.Get(ctx, input.InstanceID, domain.Selection{Tag: *input.Tag, MaxSampleAgeSeconds: input.Page.MaxSampleAgeSeconds})
	if err != nil {
		return failure(request, err)
	}
	data, err := jsonwire.EncodeObservation(result)
	if err != nil {
		return failure(request, err)
	}
	request.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	return request.Send(data)
}
func (h Handler) List(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	input, err := decode(request, true)
	if err != nil {
		return failure(request, err)
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.Timeout)
	defer cancel()
	result, err := h.Service.List(ctx, input.InstanceID, input.Page)
	if err != nil {
		return failure(request, err)
	}
	data, err := jsonwire.EncodePage(result)
	if err != nil {
		return failure(request, err)
	}
	request.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	return request.Send(data)
}
func failure(request fiber.Ctx, err error) error {
	code := jsonwire.ErrorCode(err)
	status := fiber.StatusServiceUnavailable
	switch code {
	case "invalid_argument":
		status = fiber.StatusBadRequest
	case "not_found":
		status = fiber.StatusNotFound
	case "deadline_exceeded":
		status = fiber.StatusGatewayTimeout
	case "api_not_configured", "unsupported", "not_running", "closed", "execution_changed", "continuation_expired":
		status = fiber.StatusConflict
	case "resource_exhausted":
		status = fiber.StatusTooManyRequests
	}
	request.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	return request.Status(status).Send(jsonwire.Failure(err))
}
