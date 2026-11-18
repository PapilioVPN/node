// Package httpapi exposes authenticated filesystem observations through Fiber.
package httpapi

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/storage/filesystems/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/storage/filesystems/domain"
	"github.com/gofiber/fiber/v3"
)

type Handler struct {
	Service services.Service
	Timeout time.Duration
}

func (handler Handler) Register(router fiber.Router) {
	router.Get("/v1/system/storage/filesystems", handler.List)
	router.Get("/v1/system/storage/filesystems/usage", handler.Usage)
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

func (handler Handler) Usage(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	args := request.Request().URI().QueryArgs()
	if len(request.Body()) != 0 || args.Len() != 1 || string(args.Peek("path")) == "" {
		return readError(request, domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(request.Context(), handler.Timeout)
	defer cancel()
	result, err := handler.Service.Usage(ctx, string(args.Peek("path")))
	if err != nil {
		return readError(request, err)
	}
	return request.JSON(usageResult(result))
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
		case "after_id":
			parsed, cause := strconv.ParseUint(string(value), 10, 32)
			if cause != nil {
				err = domain.ErrInvalidArgument
				return
			}
			selection.AfterID = uint32(parsed)
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
	case errors.Is(err, domain.ErrResourceExhausted):
		code, status = "resource_exhausted", fiber.StatusServiceUnavailable
	}
	return request.Status(status).JSON(fiber.Map{"error": code})
}
