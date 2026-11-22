// Package httpapi exposes scoped sync resources to authenticated controllers.
package httpapi

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/app/ports"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/domain"
	v1 "github.com/PapilioVPN/node/internal/apps/xray/users/sync/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/infra/wire"
	"github.com/gofiber/fiber/v3"
	"google.golang.org/protobuf/proto"
)

type Handler struct {
	Service   ports.Operations
	Transfers *services.Transfers
	Timeout   time.Duration
}

func (h Handler) Register(app *fiber.App) {
	base := "/v1/xray/users/instances/:id/sync"
	app.Get(base+"/revision", h.Revision)
	app.Post(base+"/plans", h.Preview)
	app.Post(base+"/applications", h.Apply)
	app.Get(base+"/operations/:operation", h.Status)
	app.Post(base+"/operations/:operation/observations", h.Refresh)
	app.Post(base+"/operations/:operation/recoveries", h.Recover)
	app.Post(base+"/transfers", h.Begin)
	app.Post(base+"/transfers/:transfer/chunks", h.Append)
	app.Post(base+"/transfers/:transfer/previews", h.Finish)
	app.Delete(base+"/transfers/:transfer", h.Discard)
}
func failure(c fiber.Ctx, err error) error {
	code := wire.Code(err)
	status := 503
	switch code {
	case "invalid_argument":
		status = 400
	case "not_found":
		status = 404
	case "conflict":
		status = 409
	case "failed_precondition":
		status = 412
	case "resource_exhausted":
		status = 429
	case "cancelled":
		status = 408
	case "deadline_exceeded":
		status = 504
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.Status(status).JSON(map[string]any{"error": map[string]string{"code": code, "message": code}})
}
func send(c fiber.Ctx, message proto.Message, err error) error {
	c.Set(fiber.HeaderCacheControl, "no-store")
	if err != nil {
		return failure(c, err)
	}
	data, err := wire.Encode(message)
	if err != nil {
		return failure(c, err)
	}
	c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	return c.Send(data)
}
func decode(c fiber.Ctx, message proto.Message) error {
	if len(c.Request().URI().QueryString()) != 0 || strings.TrimSpace(strings.Split(c.Get(fiber.HeaderContentType), ";")[0]) != fiber.MIMEApplicationJSON {
		return domain.ErrInvalid
	}
	return wire.Decode(c.Body(), message)
}
func empty(c fiber.Ctx) error {
	if len(c.Body()) != 0 || len(c.Request().URI().QueryString()) != 0 {
		return domain.ErrInvalid
	}
	return nil
}
func receipt(c fiber.Ctx, p domain.ReceiptPage, err error) error {
	if p.Receipt.OperationID == "" && err != nil {
		return failure(c, err)
	}
	return send(c, wire.Result(p, err), nil)
}
func (h Handler) Revision(c fiber.Ctx) error {
	if err := empty(c); err != nil {
		return failure(c, err)
	}
	ctx, cancel := context.WithTimeout(c.Context(), h.Timeout)
	defer cancel()
	r, err := h.Service.Revision(ctx, c.Params("id"))
	return send(c, wire.Revision(r), err)
}
func (h Handler) Preview(c fiber.Ctx) error {
	var r v1.SyncRequest
	if err := decode(c, &r); err != nil {
		return failure(c, err)
	}
	if r.InstanceId != "" {
		return failure(c, domain.ErrInvalid)
	}
	r.InstanceId = c.Params("id")
	ctx, cancel := context.WithTimeout(c.Context(), h.Timeout)
	defer cancel()
	input, err := wire.Request(ctx, &r)
	if err != nil {
		return failure(c, err)
	}
	p, err := h.Service.Preview(ctx, input)
	return send(c, wire.Preview(p), err)
}
func (h Handler) Apply(c fiber.Ctx) error {
	var r v1.ApplyRequest
	if err := decode(c, &r); err != nil {
		return failure(c, err)
	}
	if r.InstanceId != "" {
		return failure(c, domain.ErrInvalid)
	}
	revision, err := wire.ReadRevision(r.Expected)
	if err != nil {
		return failure(c, err)
	}
	ctx, cancel := context.WithTimeout(c.Context(), h.Timeout)
	defer cancel()
	p, err := h.Service.Apply(ctx, c.Params("id"), r.PlanId, revision)
	return receipt(c, p, err)
}
func (h Handler) Status(c fiber.Ctx) error {
	if len(c.Body()) != 0 || c.Request().URI().QueryArgs().Len() > 1 {
		return failure(c, domain.ErrInvalid)
	}
	offset := 0
	if c.Request().URI().QueryArgs().Has("offset") {
		v, err := strconv.ParseUint(c.Query("offset"), 10, 32)
		if err != nil {
			return failure(c, domain.ErrInvalid)
		}
		offset = int(v)
	} else if len(c.Request().URI().QueryString()) != 0 {
		return failure(c, domain.ErrInvalid)
	}
	ctx, cancel := context.WithTimeout(c.Context(), h.Timeout)
	defer cancel()
	p, err := h.Service.Status(ctx, c.Params("id"), c.Params("operation"), offset)
	return receipt(c, p, err)
}
func (h Handler) Refresh(c fiber.Ctx) error {
	if err := empty(c); err != nil {
		return failure(c, err)
	}
	ctx, cancel := context.WithTimeout(c.Context(), h.Timeout)
	defer cancel()
	p, err := h.Service.Refresh(ctx, c.Params("id"), c.Params("operation"))
	return receipt(c, p, err)
}
func (h Handler) Recover(c fiber.Ctx) error {
	var r v1.RecoveryRequest
	if err := decode(c, &r); err != nil {
		return failure(c, err)
	}
	if r.InstanceId != "" || r.OperationId != "" || r.ExpectedConfig == nil {
		return failure(c, domain.ErrInvalid)
	}
	ctx, cancel := context.WithTimeout(c.Context(), h.Timeout)
	defer cancel()
	p, err := h.Service.Recover(ctx, c.Params("id"), c.Params("operation"), *r.ExpectedConfig)
	return receipt(c, p, err)
}
func (h Handler) Begin(c fiber.Ctx) error {
	var r v1.BeginRequest
	if err := decode(c, &r); err != nil {
		return failure(c, err)
	}
	if r.Request == nil || r.Request.InstanceId != "" {
		return failure(c, domain.ErrInvalid)
	}
	r.Request.InstanceId = c.Params("id")
	ctx, cancel := context.WithTimeout(c.Context(), h.Timeout)
	defer cancel()
	input, err := wire.Request(ctx, r.Request)
	if err != nil {
		return failure(c, err)
	}
	st, err := h.Transfers.Begin(ctx, input, int(r.Items), int(r.Bytes))
	return send(c, wire.Transfer(st), err)
}
func (h Handler) Append(c fiber.Ctx) error {
	var r v1.ChunkRequest
	if err := decode(c, &r); err != nil {
		return failure(c, err)
	}
	if r.InstanceId != "" || r.TransferId != "" {
		return failure(c, domain.ErrInvalid)
	}
	chunk, err := wire.Chunk(&r)
	if err != nil {
		return failure(c, err)
	}
	ctx, cancel := context.WithTimeout(c.Context(), h.Timeout)
	defer cancel()
	st, err := h.Transfers.Append(ctx, c.Params("id"), c.Params("transfer"), chunk)
	return send(c, wire.Transfer(st), err)
}
func (h Handler) Finish(c fiber.Ctx) error {
	if err := empty(c); err != nil {
		return failure(c, err)
	}
	ctx, cancel := context.WithTimeout(c.Context(), h.Timeout)
	defer cancel()
	input, err := h.Transfers.Finish(ctx, c.Params("id"), c.Params("transfer"))
	if err != nil {
		return failure(c, err)
	}
	p, err := h.Service.Preview(ctx, input)
	return send(c, wire.Preview(p), err)
}
func (h Handler) Discard(c fiber.Ctx) error {
	if err := empty(c); err != nil {
		return failure(c, err)
	}
	ctx, cancel := context.WithTimeout(c.Context(), h.Timeout)
	defer cancel()
	return send(c, &v1.Empty{}, h.Transfers.Discard(ctx, c.Params("id"), c.Params("transfer")))
}
