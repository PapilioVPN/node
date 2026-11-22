package httpapi

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/infra/jsonwire"
	"github.com/gofiber/fiber/v3"
)

func pageInput(r fiber.Ctx) (domain.PageRequest, error) {
	r.Set(fiber.HeaderCacheControl, "no-store")
	if len(r.Body()) != 0 {
		return domain.PageRequest{}, domain.ErrInvalidArgument
	}
	var result domain.PageRequest
	valid := true
	seen := map[string]bool{}
	r.Request().URI().QueryArgs().VisitAll(func(key, value []byte) {
		name := string(key)
		if seen[name] {
			valid = false
		}
		seen[name] = true
		switch name {
		case "page_size":
			n, err := strconv.ParseUint(string(value), 10, 32)
			if err != nil || n > 50 {
				valid = false
			}
			result.Size = uint32(n)
		case "cursor":
			result.Cursor = string(value)
		default:
			valid = false
		}
	})
	if !valid {
		return domain.PageRequest{}, domain.ErrInvalidArgument
	}
	return result, nil
}
func (h Handler) Summaries(r fiber.Ctx) error {
	input, err := pageInput(r)
	if err != nil {
		return failure(r, err)
	}
	ctx, cancel := context.WithTimeout(r.Context(), h.Timeout)
	defer cancel()
	result, err := h.Catalog.List(ctx, r.Params("id"), input)
	if err != nil {
		return failure(r, err)
	}
	data, err := jsonwire.EncodeSummaries(result)
	return send(r, data, err)
}
func (h Handler) Compatibility(r fiber.Ctx) error {
	r.Set(fiber.HeaderCacheControl, "no-store")
	args := r.Request().URI().QueryArgs()
	if args.Len() != 1 || !args.Has("tag") || len(r.Body()) != 0 {
		return failure(r, domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(r.Context(), h.Timeout)
	defer cancel()
	result, err := h.Catalog.Compatibility(ctx, r.Params("id"), r.Query("tag"))
	if err != nil {
		return failure(r, err)
	}
	data, err := jsonwire.EncodeCompatibility(result)
	return send(r, data, err)
}
func replacementInput(r fiber.Ctx) (jsonwire.Request, error) {
	r.Set(fiber.HeaderCacheControl, "no-store")
	if len(r.Request().URI().QueryString()) != 0 || strings.TrimSpace(strings.Split(r.Get(fiber.HeaderContentType), ";")[0]) != fiber.MIMEApplicationJSON {
		return jsonwire.Request{}, domain.ErrInvalidArgument
	}
	return jsonwire.Decode(r.Body())
}
func (h Handler) Plan(r fiber.Ctx) error {
	input, err := replacementInput(r)
	if err != nil {
		return failure(r, err)
	}
	if input.InstanceID != nil || input.Tag == nil || !input.HasConfiguration || input.HasSources || input.HasPage() || input.Token != nil {
		return failure(r, domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(r.Context(), h.Timeout)
	defer cancel()
	selected := domain.ReplacementInput{Tag: *input.Tag, Configuration: input.Configuration}
	if input.AllowInterruption != nil {
		selected.AllowInterruption = *input.AllowInterruption
	}
	if input.DisconnectExisting != nil {
		selected.DisconnectExisting = *input.DisconnectExisting
	}
	result, err := h.Catalog.Plan(ctx, r.Params("id"), selected)
	if err != nil {
		return failure(r, err)
	}
	data, err := json.Marshal(jsonwire.PlanResult(result))
	return send(r, data, err)
}
func (h Handler) Replace(r fiber.Ctx) error {
	input, err := replacementInput(r)
	if err != nil {
		return failure(r, err)
	}
	if input.InstanceID != nil || input.Tag != nil || input.HasConfiguration || input.HasSources || input.HasPage() || input.Token == nil || input.AllowInterruption != nil || input.DisconnectExisting != nil {
		return failure(r, domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(r.Context(), h.Timeout)
	defer cancel()
	result, err := h.Catalog.Replace(ctx, r.Params("id"), *input.Token)
	return mutation(r, result, err)
}
