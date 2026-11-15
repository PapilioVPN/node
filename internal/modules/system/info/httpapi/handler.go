// Package httpapi presents node information through the native Fiber API.
package httpapi

import (
	"context"
	"errors"

	"github.com/gofiber/fiber/v3"

	"github.com/PapilioVPN/node/internal/modules/system/info/domain"
)

type InfoReader interface {
	GetNodeInfo(context.Context) (domain.NodeInfo, error)
}
type Handler struct{ Info InfoReader }

func (h Handler) Register(router fiber.Router) { router.Get("/v1/node", h.GetNodeInfo) }

func (h Handler) GetNodeInfo(c fiber.Ctx) error {
	c.Set("Cache-Control", "no-store")
	tls := c.RequestCtx().TLSConnectionState()
	if tls == nil || len(tls.VerifiedChains) == 0 {
		return fiber.NewError(fiber.StatusUnauthorized, "verified client certificate required")
	}
	info, err := h.Info.GetNodeInfo(c.Context())
	if err != nil {
		code := fiber.StatusInternalServerError
		if errors.Is(err, context.DeadlineExceeded) {
			code = fiber.StatusGatewayTimeout
		}
		if errors.Is(err, context.Canceled) {
			code = fiber.StatusRequestTimeout
		}
		return fiber.NewError(code, "cannot read node information")
	}
	return c.JSON(struct {
		NodeID       domain.NodeID       `json:"node_id"`
		Version      string              `json:"version"`
		APIVersion   string              `json:"api_version"`
		Capabilities []domain.Capability `json:"capabilities"`
	}{info.NodeID, info.Version, info.APIVersion, info.Capabilities})
}
