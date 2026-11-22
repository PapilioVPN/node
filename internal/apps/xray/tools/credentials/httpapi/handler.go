// Package httpapi exposes request-scoped native Xray credential artifacts.
package httpapi

import (
	"context"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/credentials/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/credentials/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/credentials/infra/jsonwire"
	"github.com/gofiber/fiber/v3"
	"time"
)

type Handler struct {
	Service *services.Service
	Timeout time.Duration
}

func (h Handler) Register(router fiber.Router) {
	router.Post("/v1/xray/tools/instances/:id/credentials/uuids", h.UUID)
	router.Post("/v1/xray/tools/instances/:id/credentials/x25519-keypairs", h.X25519)
	router.Post("/v1/xray/tools/instances/:id/credentials/wireguard-keypairs", h.WireGuard)
	router.Post("/v1/xray/tools/instances/:id/credentials/mldsa65-keypairs", h.MLDSA65)
	router.Post("/v1/xray/tools/instances/:id/credentials/mlkem768-keypairs", h.MLKEM768)
	router.Post("/v1/xray/tools/instances/:id/credentials/vless-encryption-pairs", h.VLESSEncryption)
	router.Post("/v1/xray/tools/instances/:id/credentials/tls-certificates", h.Certificate)
	router.Post("/v1/xray/tools/instances/:id/credentials/tls-certificate-hashes", h.CertificateHashes)
	router.Post("/v1/xray/tools/instances/:id/credentials/ech-keysets", h.ECH)
}
func (h Handler) UUID(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	if len(request.Request().URI().QueryString()) != 0 {
		return failure(request, domain.ErrInvalidArgument)
	}
	id, input, err := jsonwire.DecodeUUID(request.Body(), request.Params("id"))
	if err != nil {
		return failure(request, err)
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.Timeout)
	defer cancel()
	result, err := h.Service.UUID(ctx, id, input)
	if err != nil {
		return failure(request, err)
	}
	data, err := jsonwire.EncodeUUID(result)
	if err != nil {
		return failure(request, err)
	}
	request.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	return request.Send(data)
}
func (h Handler) X25519(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	if len(request.Request().URI().QueryString()) != 0 {
		return failure(request, domain.ErrInvalidArgument)
	}
	id, input, err := jsonwire.DecodeX25519(request.Body(), request.Params("id"))
	if err != nil {
		return failure(request, err)
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.Timeout)
	defer cancel()
	result, err := h.Service.X25519(ctx, id, input)
	if err != nil {
		return failure(request, err)
	}
	data, err := jsonwire.EncodeCurve(result)
	if err != nil {
		return failure(request, err)
	}
	request.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	return request.Send(data)
}
func (h Handler) WireGuard(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	if len(request.Request().URI().QueryString()) != 0 {
		return failure(request, domain.ErrInvalidArgument)
	}
	id, input, err := jsonwire.DecodeWireGuard(request.Body(), request.Params("id"))
	if err != nil {
		return failure(request, err)
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.Timeout)
	defer cancel()
	result, err := h.Service.WireGuard(ctx, id, input)
	if err != nil {
		return failure(request, err)
	}
	data, err := jsonwire.EncodeCurve(result)
	if err != nil {
		return failure(request, err)
	}
	request.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	return request.Send(data)
}
func (h Handler) MLDSA65(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	if len(request.Request().URI().QueryString()) != 0 {
		return failure(request, domain.ErrInvalidArgument)
	}
	id, input, err := jsonwire.DecodeSeed(request.Body(), request.Params("id"))
	if err != nil {
		return failure(request, err)
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.Timeout)
	defer cancel()
	result, err := h.Service.MLDSA65(ctx, id, input)
	if err != nil {
		return failure(request, err)
	}
	data, err := jsonwire.EncodeMLDSA65(result)
	if err != nil {
		return failure(request, err)
	}
	request.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	return request.Send(data)
}
func (h Handler) MLKEM768(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	if len(request.Request().URI().QueryString()) != 0 {
		return failure(request, domain.ErrInvalidArgument)
	}
	id, input, err := jsonwire.DecodeSeed(request.Body(), request.Params("id"))
	if err != nil {
		return failure(request, err)
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.Timeout)
	defer cancel()
	result, err := h.Service.MLKEM768(ctx, id, input)
	if err != nil {
		return failure(request, err)
	}
	data, err := jsonwire.EncodeMLKEM768(result)
	if err != nil {
		return failure(request, err)
	}
	request.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	return request.Send(data)
}
func (h Handler) VLESSEncryption(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	if len(request.Request().URI().QueryString()) != 0 {
		return failure(request, domain.ErrInvalidArgument)
	}
	id, err := jsonwire.DecodeEmpty(request.Body(), request.Params("id"))
	if err != nil {
		return failure(request, err)
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.Timeout)
	defer cancel()
	result, err := h.Service.VLESSEncryption(ctx, id)
	if err != nil {
		return failure(request, err)
	}
	data, err := jsonwire.EncodeVLESS(result)
	if err != nil {
		return failure(request, err)
	}
	request.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	return request.Send(data)
}
func (h Handler) Certificate(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	if len(request.Request().URI().QueryString()) != 0 {
		return failure(request, domain.ErrInvalidArgument)
	}
	id, input, err := jsonwire.DecodeCertificate(request.Body(), request.Params("id"))
	if err != nil {
		return failure(request, err)
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.Timeout)
	defer cancel()
	result, err := h.Service.Certificate(ctx, id, input)
	if err != nil {
		return failure(request, err)
	}
	data, err := jsonwire.EncodeCertificate(result)
	if err != nil {
		return failure(request, err)
	}
	request.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	return request.Send(data)
}
func (h Handler) CertificateHashes(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	if len(request.Request().URI().QueryString()) != 0 {
		return failure(request, domain.ErrInvalidArgument)
	}
	id, input, err := jsonwire.DecodeCertificateHashes(request.Body(), request.Params("id"))
	if err != nil {
		return failure(request, err)
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.Timeout)
	defer cancel()
	result, err := h.Service.CertificateHashes(ctx, id, input)
	if err != nil {
		return failure(request, err)
	}
	data, err := jsonwire.EncodeCertificateHashes(result)
	if err != nil {
		return failure(request, err)
	}
	request.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	return request.Send(data)
}
func (h Handler) ECH(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	if len(request.Request().URI().QueryString()) != 0 {
		return failure(request, domain.ErrInvalidArgument)
	}
	id, input, err := jsonwire.DecodeECH(request.Body(), request.Params("id"))
	if err != nil {
		return failure(request, err)
	}
	ctx, cancel := context.WithTimeout(request.Context(), h.Timeout)
	defer cancel()
	result, err := h.Service.ECH(ctx, id, input)
	if err != nil {
		return failure(request, err)
	}
	data, err := jsonwire.EncodeECH(result)
	if err != nil {
		return failure(request, err)
	}
	request.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	return request.Send(data)
}
func failure(request fiber.Ctx, err error) error {
	code := jsonwire.ErrorCode(err)
	status := fiber.StatusServiceUnavailable
	switch code {
	case "invalid_argument":
		status = fiber.StatusBadRequest
	case "not_found":
		status = fiber.StatusNotFound
	case "facility_not_configured", "native_rejected":
		status = fiber.StatusConflict
	case "resource_exhausted":
		status = fiber.StatusTooManyRequests
	case "deadline_exceeded":
		status = fiber.StatusGatewayTimeout
	case "invalid_native_output":
		status = fiber.StatusBadGateway
	}
	return request.Status(status).JSON(struct {
		Error string `json:"error"`
	}{code})
}
