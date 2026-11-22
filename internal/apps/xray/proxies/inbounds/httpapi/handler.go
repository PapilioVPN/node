// Package httpapi exposes live inbound resources through Fiber.
package httpapi

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/infra/jsonwire"
	"github.com/gofiber/fiber/v3"
)

type Handler struct {
	Catalog *services.Catalog
	Service *services.Service
	Sources *services.Sources
	Timeout time.Duration
}

func (h Handler) Register(router fiber.Router) {
	router.Get("/v1/xray/proxies/instances/:id/inbounds", h.Records)
	router.Get("/v1/xray/proxies/instances/:id/inbound-summaries", h.Summaries)
	router.Get("/v1/xray/proxies/instances/:id/inbounds/compatibility", h.Compatibility)
	router.Post("/v1/xray/proxies/instances/:id/inbounds/replacement-plans", h.Plan)
	router.Post("/v1/xray/proxies/instances/:id/inbounds/replacements", h.Replace)
	router.Get("/v1/xray/proxies/instances/:id/inbound-tags", h.Tags)
	router.Post("/v1/xray/proxies/instances/:id/inbounds", h.Add)
	router.Delete("/v1/xray/proxies/instances/:id/inbounds", h.Remove)
	router.Get("/v1/xray/proxies/instances/:id/inbounds/control-status", h.Status)
}
func noInput(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	if len(request.Request().URI().QueryString()) != 0 || len(request.Body()) != 0 {
		return domain.ErrInvalidArgument
	}
	return nil
}
func (h Handler) Records(request fiber.Ctx) error {
	input, err := pageInput(request)
	if err != nil {
		return failure(request, err)
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.Timeout)
	defer cancel()
	result, err := h.Service.RecordPage(ctx, request.Params("id"), input)
	if err != nil {
		return failure(request, err)
	}
	data, err := jsonwire.EncodeRecords(result)
	return send(request, data, err)
}
func (h Handler) Tags(request fiber.Ctx) error {
	input, err := pageInput(request)
	if err != nil {
		return failure(request, err)
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.Timeout)
	defer cancel()
	result, err := h.Service.TagPage(ctx, request.Params("id"), input)
	if err != nil {
		return failure(request, err)
	}
	data, err := jsonwire.EncodeTags(result)
	return send(request, data, err)
}
func (h Handler) Status(request fiber.Ctx) error {
	if err := noInput(request); err != nil {
		return failure(request, err)
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.Timeout)
	defer cancel()
	result, err := h.Service.Status(ctx, request.Params("id"))
	if err != nil {
		return failure(request, err)
	}
	data, err := jsonwire.EncodeStatus(result)
	return send(request, data, err)
}
func (h Handler) Add(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	if len(request.Request().URI().QueryString()) != 0 {
		return failure(request, domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.Timeout)
	defer cancel()
	contentType := strings.TrimSpace(strings.Split(request.Get(fiber.HeaderContentType), ";")[0])
	if contentType == fiber.MIMEApplicationJSON {
		input, err := jsonwire.Decode(request.Body())
		if err != nil {
			return failure(request, err)
		}
		if input.InstanceID != nil || input.Tag == nil || !input.HasSources || input.HasConfiguration || input.HasPage() || input.HasReplacement() {
			return failure(request, domain.ErrInvalidArgument)
		}
		result, err := h.Sources.Add(ctx, request.Params("id"), *input.Tag, input.Sources)
		return mutation(request, result, err)
	}
	if contentType != "application/x-protobuf" {
		return failure(request, domain.ErrInvalidArgument)
	}
	result, err := h.Service.Add(ctx, request.Params("id"), request.Body())
	return mutation(request, result, err)
}
func (h Handler) Remove(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	args := request.Request().URI().QueryArgs()
	if args.Len() != 1 || !args.Has("tag") || len(request.Body()) != 0 {
		return failure(request, domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.Timeout)
	defer cancel()
	result, err := h.Service.Remove(ctx, request.Params("id"), request.Query("tag"))
	return mutation(request, result, err)
}
func mutation(request fiber.Ctx, result domain.Operation, err error) error {
	code := ""
	if err != nil {
		var status int
		code, status = errorStatus(err)
		request.Status(status)
	}
	data, err := jsonwire.EncodeOperation(result, code)
	return send(request, data, err)
}
func send(request fiber.Ctx, data []byte, err error) error {
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
	case errors.Is(err, domain.ErrInboundNotFound):
		code, status = "inbound_not_found", fiber.StatusNotFound
	case errors.Is(err, domain.ErrAmbiguousInbound):
		code, status = "ambiguous_inbound", fiber.StatusConflict
	case errors.Is(err, domain.ErrNotFound):
		code, status = "not_found", fiber.StatusNotFound
	case errors.Is(err, domain.ErrUnsupported), errors.Is(err, domain.ErrNotRunning):
		code, status = "failed_precondition", fiber.StatusConflict
	case errors.Is(err, domain.ErrUncertain):
		code, status = "restart_required", fiber.StatusConflict
	case errors.Is(err, domain.ErrStale):
		code, status = "stale_observation", fiber.StatusConflict
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
