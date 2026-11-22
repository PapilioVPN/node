// Package httpapi adapts asset operations on the common controller mTLS server.
package httpapi

import (
	"context"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/assets/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/assets/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/assets/infra/jsonwire"
	"github.com/gofiber/fiber/v3"
	"time"
)

type Handler struct {
	Service *services.Service
	Timeout time.Duration
}

func (h Handler) Register(router fiber.Router) {
	root := "/v1/xray/tools/instances/:id/assets"
	router.Get(root, h.Inspect)
	router.Post(root+"/candidates", h.Stage)
	router.Post(root+"/activations", h.Activate)
	router.Post(root+"/rollbacks", h.Rollback)
	router.Post(root+"/recoveries", h.Recover)
}
func (h Handler) context(c fiber.Ctx) (context.Context, context.CancelFunc, error) {
	c.Set(fiber.HeaderCacheControl, "no-store")
	if len(c.Request().URI().QueryString()) != 0 {
		return nil, nil, domain.ErrInvalidArgument
	}
	ctx, cancel := context.WithTimeout(c.Context(), h.Timeout)
	return ctx, cancel, nil
}
func failure(c fiber.Ctx, err error) error {
	code := jsonwire.ErrorCode(err)
	status := 503
	switch code {
	case "invalid_argument":
		status = 400
	case "not_found":
		status = 404
	case "busy", "resource_exhausted":
		status = 429
	case "conflict", "recovery_required":
		status = 409
	case "failed_precondition", "native_rejected":
		status = 422
	case "deadline_exceeded":
		status = 504
	case "canceled":
		status = 408
	}
	c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	return c.Status(status).Send(jsonwire.Failure(err))
}
func send(c fiber.Ctx, v any) error {
	c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	return c.Send(jsonwire.Encode(v))
}
func (h Handler) Inspect(c fiber.Ctx) error {
	ctx, cancel, err := h.context(c)
	if err != nil {
		return failure(c, err)
	}
	defer cancel()
	if len(c.Body()) != 0 {
		return failure(c, domain.ErrInvalidArgument)
	}
	r, err := h.Service.Inspect(ctx, c.Params("id"))
	if err != nil {
		return failure(c, err)
	}
	return send(c, r)
}
func (h Handler) Stage(c fiber.Ctx) error {
	ctx, cancel, err := h.context(c)
	if err != nil {
		return failure(c, err)
	}
	defer cancel()
	id, q, err := jsonwire.Stage(c.Body(), c.Params("id"))
	if err != nil {
		return failure(c, err)
	}
	r, err := h.Service.Stage(ctx, id, q)
	if err != nil {
		return failure(c, err)
	}
	return send(c, r)
}
func (h Handler) Activate(c fiber.Ctx) error {
	ctx, cancel, err := h.context(c)
	if err != nil {
		return failure(c, err)
	}
	defer cancel()
	id, q, err := jsonwire.Activation(c.Body(), c.Params("id"), true)
	if err != nil {
		return failure(c, err)
	}
	r, err := h.Service.Activate(ctx, id, q)
	return send(c, jsonwire.ActivationResult{Receipt: r, ErrorCode: jsonwire.ErrorCode(err)})
}
func (h Handler) Rollback(c fiber.Ctx) error {
	ctx, cancel, err := h.context(c)
	if err != nil {
		return failure(c, err)
	}
	defer cancel()
	id, q, err := jsonwire.Activation(c.Body(), c.Params("id"), false)
	if err != nil {
		return failure(c, err)
	}
	r, err := h.Service.Rollback(ctx, id, q)
	return send(c, jsonwire.ActivationResult{Receipt: r, ErrorCode: jsonwire.ErrorCode(err)})
}
func (h Handler) Recover(c fiber.Ctx) error {
	ctx, cancel, err := h.context(c)
	if err != nil {
		return failure(c, err)
	}
	defer cancel()
	id, q, err := jsonwire.Activation(c.Body(), c.Params("id"), false)
	if err != nil {
		return failure(c, err)
	}
	r, err := h.Service.Recover(ctx, id, q)
	return send(c, jsonwire.ActivationResult{Receipt: r, ErrorCode: jsonwire.ErrorCode(err)})
}
