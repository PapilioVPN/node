// Package httpapi exposes focused account observations through Fiber.
package httpapi

import (
	"context"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/jsonwire"
	"github.com/gofiber/fiber/v3"
)

type Handler struct {
	Configurations *services.Configurations
	Service        *services.Service
	Sources        *services.Sources
	Timeout        time.Duration
}

func (h Handler) Register(router fiber.Router) {
	router.Get("/v1/xray/users/instances/:id/accounts", h.ListMetadata)
	router.Get("/v1/xray/users/instances/:id/accounts/exports", h.Export)
	router.Get("/v1/xray/users/instances/:id/account/metadata", h.GetMetadata)
	router.Get("/v1/xray/users/instances/:id/accounts/compatibility", h.Compatibility)
	router.Post("/v1/xray/users/instances/:id/accounts/configurations", h.PrepareConfiguration)
	router.Post("/v1/xray/users/instances/:id/accounts/additions", h.AddMany)
	router.Post("/v1/xray/users/instances/:id/accounts/removals", h.RemoveMany)
	router.Get("/v1/xray/users/instances/:id/accounts/count", h.Count)
	router.Get("/v1/xray/users/instances/:id/account", h.Get)
	router.Post("/v1/xray/users/instances/:id/accounts", h.Add)
	router.Delete("/v1/xray/users/instances/:id/account", h.Remove)
	router.Get("/v1/xray/users/instances/:id/accounts/control-status", h.Status)
}

// selection rejects duplicate/unknown query keys rather than silently choosing one.
func selection(request fiber.Ctx) (tag string, email *string, err error) {
	if len(request.Body()) != 0 {
		return "", nil, domain.ErrInvalidArgument
	}
	seen := map[string]bool{}
	request.Request().URI().QueryArgs().VisitAll(func(key, value []byte) {
		name := string(key)
		if seen[name] {
			err = domain.ErrInvalidArgument
			return
		}
		seen[name] = true
		switch name {
		case "inbound_tag":
			tag = string(value)
		case "email":
			copy := string(value)
			email = &copy
		default:
			err = domain.ErrInvalidArgument
		}
	})
	if !seen["inbound_tag"] {
		err = domain.ErrInvalidArgument
	}
	return
}
func (h Handler) Count(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	tag, email, err := selection(request)
	if err != nil || email != nil {
		return failure(request, domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.Timeout)
	defer cancel()
	result, err := h.Service.Count(ctx, request.Params("id"), tag)
	if err != nil {
		return failure(request, err)
	}
	data, err := jsonwire.EncodeCount(result)
	if err != nil {
		return failure(request, err)
	}
	request.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	return request.Send(data)
}
func (h Handler) Get(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	tag, email, err := selection(request)
	if err != nil || email == nil {
		return failure(request, domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.Timeout)
	defer cancel()
	result, err := h.Service.Get(ctx, request.Params("id"), tag, *email)
	if err != nil {
		return failure(request, err)
	}
	data, err := jsonwire.EncodeAccount(result)
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
	case errors.Is(err, domain.ErrNotAttempted):
		code, status = "not_attempted", fiber.StatusConflict
	case errors.Is(err, domain.ErrStale):
		code, status = "stale_observation", fiber.StatusConflict
	case errors.Is(err, domain.ErrIncompatible):
		code, status = "incompatible_account", fiber.StatusConflict
	case errors.Is(err, domain.ErrInboundMissing):
		code, status = "inbound_not_found", fiber.StatusNotFound
	case errors.Is(err, domain.ErrInboundAmbiguous):
		code, status = "ambiguous_inbound", fiber.StatusConflict
	case errors.Is(err, domain.ErrInvalidArgument):
		code, status = "invalid_argument", fiber.StatusBadRequest
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
