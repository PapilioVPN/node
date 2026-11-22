package httpapi

import (
	"context"
	"strconv"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/jsonwire"
	"github.com/gofiber/fiber/v3"
)

func viewSelection(r fiber.Ctx) (jsonwire.Selection, error) {
	r.Set(fiber.HeaderCacheControl, "no-store")
	result := jsonwire.Selection{InstanceID: r.Params("id")}
	if len(r.Body()) != 0 {
		return result, domain.ErrInvalidArgument
	}
	valid := true
	seen := map[string]bool{}
	r.Request().URI().QueryArgs().VisitAll(func(key, value []byte) {
		name := string(key)
		if seen[name] {
			valid = false
		}
		seen[name] = true
		text := string(value)
		switch name {
		case "inbound_tag":
			result.InboundTag = text
		case "email":
			result.Email = &text
		case "account_type":
			result.AccountType = &text
		case "cursor":
			result.Cursor = &text
		case "page_size":
			n, err := strconv.ParseUint(text, 10, 32)
			if err != nil || n > 50 {
				valid = false
			}
			size := uint32(n)
			result.PageSize = &size
		default:
			valid = false
		}
	})
	if !valid || !seen["inbound_tag"] {
		return result, domain.ErrInvalidArgument
	}
	return result, nil
}
func sendView(r fiber.Ctx, data []byte, err error) error {
	if err != nil {
		return failure(r, err)
	}
	r.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	return r.Send(data)
}
func (h Handler) ListMetadata(r fiber.Ctx) error {
	input, err := viewSelection(r)
	if err != nil || input.Email != nil || input.AccountType != nil {
		return failure(r, domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(r.Context(), h.Timeout)
	defer cancel()
	result, err := h.Service.ListMetadata(ctx, input.InstanceID, input.InboundTag, input.Page())
	if err != nil {
		return failure(r, err)
	}
	data, err := jsonwire.EncodeMetadataPage(result)
	return sendView(r, data, err)
}
func (h Handler) Export(r fiber.Ctx) error {
	input, err := viewSelection(r)
	if err != nil || input.Email != nil || input.AccountType != nil {
		return failure(r, domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(r.Context(), h.Timeout)
	defer cancel()
	result, err := h.Service.Export(ctx, input.InstanceID, input.InboundTag, input.Page())
	if err != nil {
		return failure(r, err)
	}
	data, err := jsonwire.EncodeExport(result)
	return sendView(r, data, err)
}
func (h Handler) GetMetadata(r fiber.Ctx) error {
	input, err := viewSelection(r)
	if err != nil || input.Email == nil || input.AccountType != nil || input.HasPage() {
		return failure(r, domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(r.Context(), h.Timeout)
	defer cancel()
	result, err := h.Service.GetMetadata(ctx, input.InstanceID, input.InboundTag, *input.Email)
	if err != nil {
		return failure(r, err)
	}
	data, err := jsonwire.EncodeMetadata(result)
	return sendView(r, data, err)
}
func (h Handler) Compatibility(r fiber.Ctx) error {
	input, err := viewSelection(r)
	if err != nil || input.Email != nil || input.HasPage() {
		return failure(r, domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(r.Context(), h.Timeout)
	defer cancel()
	result, err := h.Service.CheckCompatibility(ctx, input.InstanceID, input.InboundTag, input.AccountType)
	if err != nil {
		return failure(r, err)
	}
	data, err := jsonwire.EncodeCompatibility(result)
	return sendView(r, data, err)
}
