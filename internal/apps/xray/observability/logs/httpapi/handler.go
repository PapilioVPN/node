// Package httpapi exposes bounded console tail and follow pulls through Fiber.
package httpapi

import (
	"context"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/logs/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/logs/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/logs/infra/jsonwire"
	"github.com/gofiber/fiber/v3"
)

type Handler struct {
	Service *services.Service
	Timeout time.Duration
}

func (h Handler) Register(router fiber.Router) {
	router.Get("/v1/xray/observability/instances/:id/logs", h.Tail)
	router.Get("/v1/xray/observability/instances/:id/logs/updates", h.Follow)
}
func decode(r fiber.Ctx, follow bool) (jsonwire.Request, error) {
	input := jsonwire.Request{InstanceID: r.Params("id")}
	if len(r.Body()) != 0 || len(r.Request().URI().QueryString()) > jsonwire.MaxRequestBytes {
		return input, domain.ErrInvalidArgument
	}
	seen := map[string]bool{}
	valid := true
	r.Request().URI().QueryArgs().VisitAll(func(k, v []byte) {
		if !utf8.Valid(k) || !utf8.Valid(v) || seen[string(k)] {
			valid = false
			return
		}
		key := string(k)
		seen[key] = true
		switch key {
		case "cursor":
			if !follow {
				valid = false
			}
			input.Cursor = string(v)
		case "limit", "wait_ms":
			if key == "wait_ms" && !follow {
				valid = false
			}
			n, err := strconv.ParseUint(string(v), 10, 32)
			if err != nil {
				valid = false
			}
			if key == "limit" {
				input.Limit = uint32(n)
			} else {
				input.WaitMilliseconds = uint32(n)
			}
		default:
			valid = false
		}
	})
	if !valid || follow && !seen["cursor"] {
		return input, domain.ErrInvalidArgument
	}
	return input, nil
}
func (h Handler) Tail(r fiber.Ctx) error   { return h.serve(r, false) }
func (h Handler) Follow(r fiber.Ctx) error { return h.serve(r, true) }
func (h Handler) serve(r fiber.Ctx, follow bool) error {
	r.Set(fiber.HeaderCacheControl, "no-store")
	r.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	input, err := decode(r, follow)
	if err != nil {
		return failure(r, err)
	}
	ctx, cancel := context.WithTimeout(r.Context(), h.Timeout)
	defer cancel()
	var p domain.Page
	if follow {
		p, err = h.Service.Follow(ctx, input.InstanceID, domain.FollowRequest{Cursor: input.Cursor, Limit: input.Limit, WaitMilliseconds: input.WaitMilliseconds})
	} else {
		p, err = h.Service.Tail(ctx, input.InstanceID, domain.TailRequest{Limit: input.Limit})
	}
	if err != nil {
		return failure(r, err)
	}
	data, err := jsonwire.Encode(p)
	if err != nil {
		return failure(r, err)
	}
	return r.Send(data)
}
func failure(r fiber.Ctx, err error) error {
	code := jsonwire.ErrorCode(err)
	status := fiber.StatusServiceUnavailable
	switch code {
	case "invalid_argument":
		status = fiber.StatusBadRequest
	case "not_found":
		status = fiber.StatusNotFound
	case "canceled":
		status = 499
	case "deadline_exceeded":
		status = fiber.StatusGatewayTimeout
	case "resource_exhausted":
		status = fiber.StatusTooManyRequests
	case "output_unavailable", "execution_changed", "closed":
		status = fiber.StatusConflict
	}
	return r.Status(status).Send(jsonwire.Failure(err))
}
