package httpapi

import (
	"context"
	"strings"

	sourcewire "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/infra/jsonwire"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/jsonwire"
	"github.com/gofiber/fiber/v3"
)

func (h Handler) PrepareConfiguration(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	if len(request.Request().URI().QueryString()) != 0 || strings.TrimSpace(strings.Split(request.Get(fiber.HeaderContentType), ";")[0]) != fiber.MIMEApplicationJSON {
		return failure(request, domain.ErrInvalidArgument)
	}
	input, err := jsonwire.DecodePreparation(request.Body())
	if err != nil {
		return failure(request, err)
	}
	if input.InstanceID != nil {
		return failure(request, domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.Timeout)
	defer cancel()
	result, err := h.Configurations.Prepare(ctx, request.Params("id"), input.Sources, input.Inbounds)
	if err != nil {
		return failure(request, err)
	}
	data, err := sourcewire.EncodeCompilation(result)
	return send(request, data, err)
}
