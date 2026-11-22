package httpapi

import (
	"context"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/infra/jsonwire"
	"github.com/gofiber/fiber/v3"
	"strings"
)

func jsonRequest(r fiber.Ctx) error {
	if err := stateRequest(r); err != nil {
		return err
	}
	if strings.TrimSpace(strings.Split(r.Get(fiber.HeaderContentType), ";")[0]) != fiber.MIMEApplicationJSON {
		return domain.ErrInvalidArgument
	}
	return nil
}
func (h Handler) Plan(r fiber.Ctx) error {
	if err := jsonRequest(r); err != nil {
		return failure(r, err)
	}
	input, err := jsonwire.DecodeState(r.Body())
	if err != nil {
		return failure(r, err)
	}
	if input.InstanceID != nil || input.ExpectedRevision == nil || !input.HasSources {
		return failure(r, domain.ErrInvalidArgument)
	}
	if h.Plans == nil {
		return failure(r, domain.ErrUnsupported)
	}
	ctx, cancel := context.WithTimeout(r.Context(), h.Timeout)
	defer cancel()
	result, err := h.Plans.Plan(ctx, r.Params("id"), *input.ExpectedRevision, input.Sources)
	if err != nil {
		return failure(r, err)
	}
	data, err := jsonwire.EncodePlan(result)
	return send(r, data, err)
}
func (h Handler) ApplyPlan(r fiber.Ctx) error {
	if err := jsonRequest(r); err != nil {
		return failure(r, err)
	}
	if _, err := jsonwire.DecodeIntent(r.Body()); err != nil {
		return failure(r, err)
	}
	if h.Plans == nil {
		return failure(r, domain.ErrUnsupported)
	}
	ctx, cancel := context.WithTimeout(r.Context(), h.Timeout)
	defer cancel()
	result, err := h.Plans.Apply(ctx, r.Params("id"), r.Params("plan"))
	if result.ID == "" && err != nil {
		return failure(r, err)
	}
	data, err := jsonwire.EncodeApplication(result)
	return send(r, data, err)
}
func (h Handler) Application(r fiber.Ctx) error {
	if err := stateRequest(r); err != nil {
		return failure(r, err)
	}
	if len(r.Body()) != 0 {
		return failure(r, domain.ErrInvalidArgument)
	}
	if h.Plans == nil {
		return failure(r, domain.ErrUnsupported)
	}
	ctx, cancel := context.WithTimeout(r.Context(), h.Timeout)
	defer cancel()
	result, err := h.Plans.Application(ctx, r.Params("id"), r.Params("application"))
	if err != nil {
		return failure(r, err)
	}
	data, err := jsonwire.EncodeApplication(result)
	return send(r, data, err)
}
func (h Handler) Recover(r fiber.Ctx) error {
	if err := jsonRequest(r); err != nil {
		return failure(r, err)
	}
	input, err := jsonwire.DecodeIntent(r.Body(), "expected_revision", "recovery_id")
	if err != nil {
		return failure(r, err)
	}
	revision, err := jsonwire.Revision(input)
	if err != nil {
		return failure(r, err)
	}
	if h.Plans == nil {
		return failure(r, domain.ErrUnsupported)
	}
	ctx, cancel := context.WithTimeout(r.Context(), h.Timeout)
	defer cancel()
	result, err := h.Plans.Recover(ctx, r.Params("id"), r.Params("application"), revision, input["recovery_id"])
	if result.ID == "" && err != nil {
		return failure(r, err)
	}
	data, err := jsonwire.EncodeApplication(result)
	return send(r, data, err)
}
