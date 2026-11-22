// Package httpapi exposes exact online observations to management controllers.
package httpapi

import (
	"context"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/users/online/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/users/online/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/online/infra/jsonwire"
	"github.com/gofiber/fiber/v3"
)

type Handler struct {
	Service *services.Service
	Timeout time.Duration
}

func (h Handler) Register(router fiber.Router) {
	router.Get("/v1/xray/users/instances/:id/online", h.Count)
	router.Get("/v1/xray/users/instances/:id/online/ips", h.IPs)
}
func selection(request fiber.Ctx) (domain.Selection, error) {
	if len(request.Body()) != 0 {
		return domain.Selection{}, domain.ErrInvalidArgument
	}
	result := domain.Selection{InstanceID: request.Params("id")}
	seen := false
	var err error
	request.Request().URI().QueryArgs().VisitAll(func(key, value []byte) {
		if string(key) != "email" || seen {
			err = domain.ErrInvalidArgument
			return
		}
		seen = true
		result.Email = string(value)
	})
	if !seen {
		err = domain.ErrInvalidArgument
	}
	return result, err
}
func (h Handler) Count(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	s, err := selection(request)
	if err != nil {
		return failure(request, err)
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.Timeout)
	defer cancel()
	result, err := h.Service.Count(ctx, s)
	if err != nil {
		return failure(request, err)
	}
	data, err := jsonwire.EncodeStatus(result)
	if err != nil {
		return failure(request, err)
	}
	request.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	return request.Send(data)
}
func (h Handler) IPs(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	s, err := selection(request)
	if err != nil {
		return failure(request, err)
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.Timeout)
	defer cancel()
	result, err := h.Service.IPs(ctx, s)
	if err != nil {
		return failure(request, err)
	}
	data, err := jsonwire.EncodeIPs(result)
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
	case "facility_not_configured", "facility_unsupported", "execution_changed":
		status = fiber.StatusConflict
	case "resource_exhausted":
		status = fiber.StatusTooManyRequests
	case "deadline_exceeded":
		status = fiber.StatusGatewayTimeout
	}
	return request.Status(status).JSON(struct {
		Error string `json:"error"`
	}{code})
}
