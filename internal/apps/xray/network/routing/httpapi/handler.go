// Package httpapi adapts live routing operations to authenticated Fiber routes.
package httpapi

import (
	"context"
	"strconv"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/infra/jsonwire"
	"github.com/gofiber/fiber/v3"
)

type Handler struct {
	Service *services.Service
	Timeout time.Duration
}

func (h Handler) Register(r fiber.Router) {
	base := "/v1/xray/network/instances/:id/routing"
	r.Get(base+"/rules", h.List)
	r.Post(base+"/rules", h.Add)
	r.Delete(base+"/rules", h.Remove)
	r.Post(base+"/tests", h.Probe)
	r.Get(base+"/balancers", h.Balancer)
	r.Put(base+"/balancers/override", h.Override)
	r.Get(base+"/control-status", h.Status)
}
func code(err error) int {
	switch jsonwire.ErrorCode(err) {
	case "":
		return 200
	case "invalid_argument":
		return 400
	case "not_found":
		return 404
	case "resource_exhausted":
		return 429
	case "failed_precondition", "restart_required", "execution_changed", "observation_expired", "engine_unavailable":
		return 409
	case "deadline_exceeded":
		return 504
	default:
		return 503
	}
}
func send(c fiber.Ctx, data []byte, err error) error {
	c.Set(fiber.HeaderCacheControl, "no-store")
	c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	return c.Status(code(err)).Send(data)
}
func failure(c fiber.Ctx, err error) error { return send(c, jsonwire.Failure(err), err) }
func query(c fiber.Ctx, keys ...string) error {
	if len(c.Body()) != 0 {
		return domain.ErrInvalidArgument
	}
	seen := map[string]bool{}
	var invalid bool
	c.Request().URI().QueryArgs().VisitAll(func(k, v []byte) {
		key := string(k)
		allowed := false
		for _, s := range keys {
			allowed = allowed || s == key
		}
		if !allowed || seen[key] {
			invalid = true
		}
		seen[key] = true
	})
	if invalid {
		return domain.ErrInvalidArgument
	}
	return nil
}
func body(c fiber.Ctx) error {
	if len(c.Request().URI().QueryString()) != 0 || c.Get(fiber.HeaderContentType) != fiber.MIMEApplicationJSON {
		return domain.ErrInvalidArgument
	}
	return nil
}
func (h Handler) List(c fiber.Ctx) error {
	if err := query(c, "page_size", "page_token"); err != nil {
		return failure(c, err)
	}
	size := 0
	if c.Query("page_size") != "" {
		v, err := strconv.Atoi(c.Query("page_size"))
		if err != nil {
			return failure(c, domain.ErrInvalidArgument)
		}
		size = v
	}
	ctx, cancel := context.WithTimeout(c.Context(), h.Timeout)
	defer cancel()
	result, err := h.Service.List(ctx, domain.PageRequest{InstanceID: c.Params("id"), PageSize: size, PageToken: c.Query("page_token")})
	if err != nil {
		return failure(c, err)
	}
	return send(c, jsonwire.EncodePage(result), nil)
}
func (h Handler) Add(c fiber.Ctx) error {
	if err := body(c); err != nil {
		return failure(c, err)
	}
	r, err := jsonwire.DecodeAdd(c.Body())
	if err != nil {
		return failure(c, err)
	}
	if r.InstanceID != "" {
		return failure(c, domain.ErrInvalidArgument)
	}
	r.InstanceID = c.Params("id")
	ctx, cancel := context.WithTimeout(c.Context(), h.Timeout)
	defer cancel()
	o, err := h.Service.Add(ctx, r)
	return send(c, jsonwire.EncodeOperation(o, err), err)
}
func (h Handler) Remove(c fiber.Ctx) error {
	if err := query(c, "tag"); err != nil {
		return failure(c, err)
	}
	ctx, cancel := context.WithTimeout(c.Context(), h.Timeout)
	defer cancel()
	o, err := h.Service.Remove(ctx, c.Params("id"), c.Query("tag"))
	return send(c, jsonwire.EncodeOperation(o, err), err)
}
func (h Handler) Probe(c fiber.Ctx) error {
	if err := body(c); err != nil {
		return failure(c, err)
	}
	r, err := jsonwire.DecodeProbe(c.Body())
	if err != nil {
		return failure(c, err)
	}
	if r.InstanceID != "" {
		return failure(c, domain.ErrInvalidArgument)
	}
	r.InstanceID = c.Params("id")
	ctx, cancel := context.WithTimeout(c.Context(), h.Timeout)
	defer cancel()
	o, err := h.Service.Probe(ctx, r)
	return send(c, jsonwire.EncodeProbe(o, err), err)
}
func (h Handler) Balancer(c fiber.Ctx) error {
	if err := query(c, "tag"); err != nil {
		return failure(c, err)
	}
	ctx, cancel := context.WithTimeout(c.Context(), h.Timeout)
	defer cancel()
	o, err := h.Service.Balancer(ctx, c.Params("id"), c.Query("tag"))
	if err != nil {
		return failure(c, err)
	}
	return send(c, jsonwire.EncodeBalancer(o), nil)
}
func (h Handler) Override(c fiber.Ctx) error {
	if err := body(c); err != nil {
		return failure(c, err)
	}
	r, err := jsonwire.DecodeOverride(c.Body())
	if err != nil {
		return failure(c, err)
	}
	if r.InstanceID != "" {
		return failure(c, domain.ErrInvalidArgument)
	}
	r.InstanceID = c.Params("id")
	ctx, cancel := context.WithTimeout(c.Context(), h.Timeout)
	defer cancel()
	o, err := h.Service.Override(ctx, r)
	return send(c, jsonwire.EncodeOperation(o, err), err)
}
func (h Handler) Status(c fiber.Ctx) error {
	if err := query(c); err != nil {
		return failure(c, err)
	}
	ctx, cancel := context.WithTimeout(c.Context(), h.Timeout)
	defer cancel()
	o, err := h.Service.Status(ctx, c.Params("id"))
	if err != nil {
		return failure(c, err)
	}
	return send(c, jsonwire.EncodeStatus(o), nil)
}
