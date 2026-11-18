// Package httpapi exposes the authenticated host identity read through Fiber.
package httpapi

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/identity/app/services"
	"github.com/gofiber/fiber/v3"
)

type Handler struct {
	Service   services.Service
	AllowRead bool
	Timeout   time.Duration
}

func (handler Handler) Register(router fiber.Router) {
	router.Get("/v1/system/host/identity", handler.Read)
}

func (handler Handler) Read(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	identity := request.RequestCtx().TLSConnectionState()
	if identity == nil || len(identity.VerifiedChains) == 0 {
		return request.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthenticated"})
	}
	if !handler.AllowRead {
		return request.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "permission_denied"})
	}
	if len(request.Body()) != 0 || len(request.Request().URI().QueryString()) != 0 {
		return request.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid_argument"})
	}
	// Fiber does not propagate ordinary client disconnects. Bound this synchronous
	// read explicitly; do not retain the pooled request context in application code.
	ctx, cancel := context.WithTimeout(request.Context(), handler.Timeout)
	defer cancel()
	result, err := handler.Service.Read(ctx)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return request.Status(fiber.StatusGatewayTimeout).JSON(fiber.Map{"error": "deadline_exceeded"})
		}
		if errors.Is(err, context.Canceled) {
			return request.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "canceled"})
		}
		return request.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "unavailable"})
	}
	return request.JSON(response{
		Identity:   identityResponse{result.Identity.Hostname, result.Identity.OperatingSystem, result.Identity.Release, result.Identity.Version, result.Identity.Machine},
		ObservedAt: result.ObservedAt, DurationNS: strconv.FormatInt(int64(result.Duration), 10),
	})
}

type response struct {
	Identity   identityResponse `json:"identity"`
	ObservedAt time.Time        `json:"observed_at"`
	DurationNS string           `json:"duration_ns"`
}

type identityResponse struct {
	Hostname        string `json:"hostname"`
	OperatingSystem string `json:"operating_system"`
	Release         string `json:"release"`
	Version         string `json:"version"`
	Machine         string `json:"machine"`
}
