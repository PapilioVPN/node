// Package httpapi exposes authenticated session observations through Fiber.
package httpapi

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/sessions/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/host/sessions/domain"
	"github.com/gofiber/fiber/v3"
)

type Handler struct {
	Service services.Service
	Timeout time.Duration
}

func (handler Handler) Register(router fiber.Router) {
	router.Get("/v1/system/host/sessions", handler.List)
	router.Get("/v1/system/host/login-records", handler.ListRecords)
	router.Get("/v1/system/host/sessions/:id", handler.Read)
}

func (handler Handler) List(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	selection, err := selectionInput(request)
	if err != nil {
		return readError(request, err)
	}
	ctx, cancel := context.WithTimeout(request.Context(), handler.Timeout)
	defer cancel()
	result, err := handler.Service.List(ctx, selection)
	if err != nil {
		return readError(request, err)
	}
	return request.JSON(listResult(result))
}

func (handler Handler) Read(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	if len(request.Body()) != 0 || len(request.Request().URI().QueryString()) != 0 {
		return readError(request, domain.ErrInvalidArgument)
	}

	ctx, cancel := context.WithTimeout(request.Context(), handler.Timeout)
	defer cancel()
	result, err := handler.Service.Read(ctx, request.Params("id"))
	if err != nil {
		return readError(request, err)
	}
	return request.JSON(readResult(result))
}

func (handler Handler) ListRecords(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	input, err := selectionInput(request)
	if err != nil {
		return readError(request, err)
	}
	ctx, cancel := context.WithTimeout(request.Context(), handler.Timeout)
	defer cancel()
	result, err := handler.Service.ListRecords(ctx, input)
	if err != nil {
		return readError(request, err)
	}
	return request.JSON(recordsResult(result))
}

func selectionInput(request fiber.Ctx) (domain.Selection, error) {
	if len(request.Body()) != 0 {
		return domain.Selection{}, domain.ErrInvalidArgument
	}
	args := request.Request().URI().QueryArgs()
	var selection domain.Selection
	var err error
	seen := map[string]bool{}
	args.VisitAll(func(key, value []byte) {
		name := string(key)
		if seen[name] {
			err = domain.ErrInvalidArgument
			return
		}
		seen[name] = true
		switch name {
		case "offset":
			parsed, cause := strconv.ParseUint(string(value), 10, 32)
			if cause != nil {
				err = domain.ErrInvalidArgument
				return
			}
			selection.Offset = uint32(parsed)
		case "limit":
			parsed, cause := strconv.ParseUint(string(value), 10, 32)
			if cause != nil {
				err = domain.ErrInvalidArgument
				return
			}
			selection.Limit = uint32(parsed)
		default:
			err = domain.ErrInvalidArgument
		}
	})
	return selection, err
}

func readError(request fiber.Ctx, err error) error {
	code, status := "unavailable", fiber.StatusServiceUnavailable
	switch {
	case errors.Is(err, domain.ErrInvalidArgument):
		code, status = "invalid_argument", fiber.StatusBadRequest
	case errors.Is(err, context.DeadlineExceeded):
		code, status = "deadline_exceeded", fiber.StatusGatewayTimeout
	case errors.Is(err, context.Canceled):
		code = "canceled"
	case errors.Is(err, domain.ErrNotFound):
		code, status = "not_found", fiber.StatusNotFound
	case errors.Is(err, domain.ErrPermissionDenied):
		code, status = "permission_denied", fiber.StatusForbidden
	case errors.Is(err, domain.ErrUnsupported):
		code, status = "unimplemented", fiber.StatusNotImplemented
	case errors.Is(err, domain.ErrResourceExhausted):
		code, status = "resource_exhausted", fiber.StatusServiceUnavailable
	}
	return request.Status(status).JSON(fiber.Map{"error": code})
}
