package httpapi

import (
	"context"
	"strconv"
	"strings"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/infra/jsonwire"
	"github.com/gofiber/fiber/v3"
)

func pageRequest(request fiber.Ctx) (domain.PageRequest, error) {
	if len(request.Body()) != 0 || len(request.Request().URI().QueryString()) > 4096 {
		return domain.PageRequest{}, domain.ErrInvalidArgument
	}
	var result domain.PageRequest
	seen := map[string]bool{}
	valid := true
	request.Request().URI().QueryArgs().VisitAll(func(k, v []byte) {
		key := string(k)
		if seen[key] {
			valid = false
		}
		seen[key] = true
		switch key {
		case "page_size":
			value, err := strconv.ParseUint(string(v), 10, 32)
			if err != nil {
				valid = false
			}
			result.PageSize = uint32(value)
		case "page_token":
			result.PageToken = string(v)
		default:
			valid = false
		}
	})
	if !valid {
		return domain.PageRequest{}, domain.ErrInvalidArgument
	}
	return result, nil
}
func (h Handler) PlanReplacement(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	if len(request.Request().URI().QueryString()) != 0 || strings.TrimSpace(strings.Split(request.Get(fiber.HeaderContentType), ";")[0]) != fiber.MIMEApplicationJSON {
		return failure(request, domain.ErrInvalidArgument)
	}
	input, err := jsonwire.DecodePlan(request.Body())
	if err != nil {
		return failure(request, err)
	}
	if input.Control.InstanceID != nil {
		return failure(request, domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.Timeout)
	defer cancel()
	var plan domain.ReplacementPlan
	if input.Control.HasSources {
		plan, err = h.Replacements.PlanSources(ctx, request.Params("id"), *input.Control.Tag, input.Control.Sources, input.DefaultPolicy, input.ConnectionPolicy)
	} else {
		plan, err = h.Replacements.Plan(ctx, request.Params("id"), domain.ReplacementRequest{Tag: *input.Control.Tag, Configuration: input.Control.Configuration, DefaultPolicy: input.DefaultPolicy, ConnectionPolicy: input.ConnectionPolicy})
	}
	if err != nil {
		return failure(request, err)
	}
	data, err := jsonwire.EncodePlan(plan)
	return send(request, data, err)
}
func (h Handler) ApplyReplacement(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	if len(request.Request().URI().QueryString()) != 0 || strings.TrimSpace(strings.Split(request.Get(fiber.HeaderContentType), ";")[0]) != fiber.MIMEApplicationJSON {
		return failure(request, domain.ErrInvalidArgument)
	}
	id, plan, err := jsonwire.DecodePlanID(request.Body())
	if err != nil {
		return failure(request, err)
	}
	if id != nil {
		return failure(request, domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.Timeout)
	defer cancel()
	result, err := h.Replacements.Apply(ctx, request.Params("id"), plan)
	if result.PlanID == "" && err != nil {
		return failure(request, err)
	}
	data, encodeErr := jsonwire.EncodeReplacement(result)
	return send(request, data, encodeErr)
}
func (h Handler) ReplacementResult(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	if len(request.Body()) != 0 || len(request.Request().URI().QueryString()) != 0 || len(request.Params("plan")) != 32 {
		return failure(request, domain.ErrInvalidArgument)
	}
	result, err := h.Replacements.Result(request.Context(), request.Params("id"), request.Params("plan"))
	if err != nil {
		return failure(request, err)
	}
	data, err := jsonwire.EncodeReplacement(result)
	return send(request, data, err)
}
