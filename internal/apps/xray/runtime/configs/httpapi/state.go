package httpapi

import (
	"context"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/infra/jsonwire"
	"github.com/gofiber/fiber/v3"
)

func stateRequest(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	if len(request.Request().URI().QueryString()) != 0 {
		return domain.ErrInvalidArgument
	}
	return nil
}
func send(request fiber.Ctx, data []byte, err error) error {
	if err != nil {
		return failure(request, err)
	}
	request.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	return request.Send(data)
}
func (h Handler) Get(request fiber.Ctx) error {
	if err := stateRequest(request); err != nil {
		return failure(request, err)
	}
	if h.State == nil {
		return failure(request, domain.ErrUnsupported)
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.Timeout)
	defer cancel()
	result, err := h.State.Read(ctx, request.Params("id"))
	if err != nil {
		return failure(request, err)
	}
	data, err := jsonwire.EncodeDesired(request.Params("id"), result)
	return send(request, data, err)
}
func (h Handler) Metadata(request fiber.Ctx) error {
	if err := stateRequest(request); err != nil {
		return failure(request, err)
	}
	if h.State == nil {
		return failure(request, domain.ErrUnsupported)
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.Timeout)
	defer cancel()
	result, err := h.State.Metadata(ctx, request.Params("id"))
	if err != nil {
		return failure(request, err)
	}
	data, err := jsonwire.EncodeMetadata(request.Params("id"), result)
	return send(request, data, err)
}
func (h Handler) Replace(request fiber.Ctx) error {
	if err := stateRequest(request); err != nil {
		return failure(request, err)
	}
	input, err := jsonwire.DecodeState(request.Body())
	if err != nil {
		return failure(request, err)
	}
	if input.InstanceID != nil || input.ExpectedRevision == nil || !input.HasSources {
		return failure(request, domain.ErrInvalidArgument)
	}
	if h.State == nil {
		return failure(request, domain.ErrUnsupported)
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.Timeout)
	defer cancel()
	result, err := h.State.Replace(ctx, request.Params("id"), *input.ExpectedRevision, input.Sources)
	return changeResult(request, result, err)
}
func (h Handler) Apply(request fiber.Ctx) error {
	if err := stateRequest(request); err != nil {
		return failure(request, err)
	}
	input, err := jsonwire.DecodeState(request.Body())
	if err != nil {
		return failure(request, err)
	}
	if input.InstanceID != nil || input.ExpectedRevision == nil || input.HasSources {
		return failure(request, domain.ErrInvalidArgument)
	}
	if h.State == nil {
		return failure(request, domain.ErrUnsupported)
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.Timeout)
	defer cancel()
	result, err := h.State.Apply(ctx, request.Params("id"), *input.ExpectedRevision)
	return changeResult(request, result, err)
}
func changeResult(request fiber.Ctx, result domain.Change, err error) error {
	code := ""
	if err != nil {
		var status int
		code, status = errorStatus(err)
		request.Status(status)
	}
	data, encodeErr := jsonwire.EncodeChange(request.Params("id"), result, code)
	return send(request, data, encodeErr)
}
