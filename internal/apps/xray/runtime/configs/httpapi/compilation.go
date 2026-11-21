package httpapi

import (
	"context"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/infra/jsonwire"
	"github.com/gofiber/fiber/v3"
)

func (h Handler) Compile(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	if len(request.Request().URI().QueryString()) != 0 {
		return failure(request, domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.Timeout)
	defer cancel()
	id, sources, err := jsonwire.DecodeCompilation(request.Body())
	if err != nil {
		return failure(request, err)
	}
	result, err := h.Service.Compile(ctx, id, sources)
	if err != nil {
		return failure(request, err)
	}
	data, err := jsonwire.EncodeCompilation(result)
	if err != nil {
		return failure(request, err)
	}
	request.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	return request.Send(data)
}
