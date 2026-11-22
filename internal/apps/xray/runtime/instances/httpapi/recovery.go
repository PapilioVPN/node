package httpapi

import (
	"context"
	"net/url"
	"strconv"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/jsonwire"
	"github.com/gofiber/fiber/v3"
)

func (h Handler) recoveryInput(request fiber.Ctx, fields, optional string) (jsonwire.Input, error) {
	request.Set(fiber.HeaderCacheControl, "no-store")
	if len(request.Request().URI().QueryString()) != 0 {
		return jsonwire.Input{}, domain.ErrInvalidArgument
	}
	return jsonwire.Decode(request.Body(), fields, optional)
}
func (h Handler) List(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	if len(request.Body()) != 0 || len(request.Request().URI().QueryString()) > 256 {
		return fail(request, domain.ErrInvalidArgument, nil)
	}
	values, err := url.ParseQuery(string(request.Request().URI().QueryString()))
	if err != nil {
		return fail(request, domain.ErrInvalidArgument, nil)
	}
	for key, v := range values {
		if (key != "after" && key != "limit") || len(v) != 1 {
			return fail(request, domain.ErrInvalidArgument, nil)
		}
	}
	var limit uint64
	if text := values.Get("limit"); text != "" {
		limit, err = strconv.ParseUint(text, 10, 32)
		if err != nil || strconv.FormatUint(limit, 10) != text {
			return fail(request, domain.ErrInvalidArgument, nil)
		}
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.Timeout)
	defer cancel()
	r, err := h.Service.List(ctx, values.Get("after"), uint32(limit))
	if err != nil {
		return fail(request, err, nil)
	}
	return request.JSON(pageResult(r))
}
func (h Handler) Lease(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	if !empty(request) {
		return fail(request, domain.ErrInvalidArgument, nil)
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.Timeout)
	defer cancel()
	r, err := h.Service.Lease(ctx, request.Params("id"))
	if err != nil {
		return fail(request, err, nil)
	}
	return request.JSON(jsonwire.Lease(r))
}
func (h Handler) Retention(request fiber.Ctx) error {
	q, err := h.recoveryInput(request, "execution_id preserve", "")
	if err != nil {
		return fail(request, err, nil)
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.Timeout)
	defer cancel()
	id := request.Params("id")
	r, err := h.Service.SetRetention(ctx, id, domain.RetentionRequest{ExecutionID: q.ExecutionID, Preserve: q.Preserve})
	if err != nil {
		return fail(request, err, &r)
	}
	return request.JSON(controlResult(r))
}
func (h Handler) Repair(request fiber.Ctx) error {
	q, err := h.recoveryInput(request, "execution_id max_attempts replace_unready", "execution_id replace_unready")
	if err != nil {
		return fail(request, err, nil)
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.Timeout)
	defer cancel()
	id := request.Params("id")
	r, err := h.Service.Repair(ctx, id, domain.RepairRequest{ExecutionID: q.ExecutionID, MaxAttempts: q.MaxAttempts, ReplaceUnready: q.ReplaceUnready})
	if err != nil {
		return failRepair(request, err, &r.Control, &r)
	}
	return request.JSON(repairResult(r))
}
func (h Handler) AcquireLease(request fiber.Ctx) error {
	q, err := h.recoveryInput(request, "expected_revision execution_id owner ttl_seconds policy", "")
	if err != nil {
		return fail(request, err, nil)
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.Timeout)
	defer cancel()
	id := request.Params("id")
	revision, err := jsonwire.Revision(q.ExpectedRevision)
	if err != nil {
		return fail(request, err, nil)
	}
	r, err := h.Service.AcquireLease(ctx, id, domain.AcquireLease{ExpectedRevision: revision, ExecutionID: q.ExecutionID, Owner: q.Owner, TTLSeconds: q.TTLSeconds, Policy: q.Policy})
	if err != nil {
		return fail(request, err, nil)
	}
	return request.JSON(jsonwire.Receipt(r))
}
func (h Handler) RenewLease(request fiber.Ctx) error {
	q, err := h.recoveryInput(request, "expected_revision token ttl_seconds", "")
	if err != nil {
		return fail(request, err, nil)
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.Timeout)
	defer cancel()
	id := request.Params("id")
	revision, err := jsonwire.Revision(q.ExpectedRevision)
	if err != nil {
		return fail(request, err, nil)
	}
	r, err := h.Service.RenewLease(ctx, id, domain.RenewLease{ExpectedRevision: revision, Token: q.Token, TTLSeconds: q.TTLSeconds})
	if err != nil {
		return fail(request, err, nil)
	}
	return request.JSON(jsonwire.Lease(r))
}
func (h Handler) ReleaseLease(request fiber.Ctx) error {
	q, err := h.recoveryInput(request, "expected_revision token", "")
	if err != nil {
		return fail(request, err, nil)
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.Timeout)
	defer cancel()
	id := request.Params("id")
	revision, err := jsonwire.Revision(q.ExpectedRevision)
	if err != nil {
		return fail(request, err, nil)
	}
	r, err := h.Service.ReleaseLease(ctx, id, domain.ReleaseLease{ExpectedRevision: revision, Token: q.Token})
	if err != nil {
		return fail(request, err, nil)
	}
	return request.JSON(jsonwire.Lease(r))
}

type pageResponse struct {
	Items     []statusResponse `json:"items"`
	NextAfter string           `json:"next_after"`
}

func pageResult(r domain.InstancePage) pageResponse {
	out := pageResponse{Items: make([]statusResponse, 0, len(r.Items)), NextAfter: r.NextAfter}
	for _, item := range r.Items {
		out.Items = append(out.Items, statusResult(item))
	}
	return out
}

type repairResponse struct {
	Control  controlResponse `json:"control"`
	Attempts uint32          `json:"attempts"`
	Ready    bool            `json:"ready"`
	Action   string          `json:"action"`
}

func repairResult(r domain.RepairResult) repairResponse {
	return repairResponse{Control: controlResult(r.Control), Attempts: r.Attempts, Ready: r.Ready, Action: r.Action}
}
