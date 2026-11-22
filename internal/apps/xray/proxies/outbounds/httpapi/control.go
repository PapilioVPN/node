package httpapi

import (
	"context"
	"strings"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/infra/jsonwire"
	"github.com/gofiber/fiber/v3"
)

func (h Handler) Status(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	if len(request.Body()) != 0 || len(request.Request().URI().QueryString()) != 0 {
		err := domain.ErrInvalidArgument
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
		input, err := jsonwire.DecodeControl(request.Body())
		if err != nil {
			return failure(request, err)
		}
		if input.InstanceID != nil || input.Tag == nil || !input.HasSources || input.HasConfiguration {
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
