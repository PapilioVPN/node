package httpapi

import (
	"context"
	"strings"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/jsonwire"
	"github.com/gofiber/fiber/v3"
)

func (h Handler) AddMany(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	if len(request.Request().URI().QueryString()) != 0 || strings.TrimSpace(strings.Split(request.Get(fiber.HeaderContentType), ";")[0]) != fiber.MIMEApplicationJSON {
		return failure(request, domain.ErrInvalidArgument)
	}
	input, err := jsonwire.DecodeAdditions(request.Body())
	if err != nil {
		return failure(request, err)
	}
	if input.InstanceID != nil {
		return failure(request, domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.Timeout)
	defer cancel()
	if len(input.Sources) != 0 {
		result, err := h.Sources.AddMany(ctx, request.Params("id"), input.Selections, input.Sources, input.Parallelism)
		return batchResult(request, result, err)
	}
	result, err := h.Service.AddMany(ctx, request.Params("id"), input.Items, input.Parallelism)
	return batchResult(request, result, err)
}
func (h Handler) RemoveMany(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	if len(request.Request().URI().QueryString()) != 0 || strings.TrimSpace(strings.Split(request.Get(fiber.HeaderContentType), ";")[0]) != fiber.MIMEApplicationJSON {
		return failure(request, domain.ErrInvalidArgument)
	}
	input, err := jsonwire.DecodeRemovals(request.Body())
	if err != nil {
		return failure(request, err)
	}
	if input.InstanceID != nil {
		return failure(request, domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.Timeout)
	defer cancel()
	result, err := h.Service.RemoveMany(ctx, request.Params("id"), input.Items, input.Parallelism)
	return batchResult(request, result, err)
}
func batchResult(request fiber.Ctx, result domain.Batch, cause error) error {
	if len(result.Items) == 0 && cause != nil {
		return failure(request, cause)
	}
	codes := make([]string, len(result.Items))
	for i, item := range result.Items {
		if item.Err != nil {
			codes[i], _ = errorStatus(item.Err)
		}
	}
	code := ""
	if cause != nil {
		code, _ = errorStatus(cause)
	}
	data, err := jsonwire.EncodeBatch(result, codes, code)
	return send(request, data, err)
}
