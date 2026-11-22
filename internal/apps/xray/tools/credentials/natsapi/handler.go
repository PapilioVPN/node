// Package natsapi exposes credential requests to broker-authorized controllers.
package natsapi

import (
	"context"
	"encoding/json"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/credentials/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/credentials/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/credentials/infra/jsonwire"
	"github.com/nats-io/nats.go"
	"time"
)

type Handler struct {
	Service *services.Service
	Timeout time.Duration
}

func deadline(ctx context.Context, headers nats.Header, timeout time.Duration) (context.Context, context.CancelFunc, error) {
	end := time.Now().Add(timeout)
	if values, ok := headers["Papilio-Deadline"]; ok {
		if len(values) != 1 {
			return nil, nil, domain.ErrInvalidArgument
		}
		requested, err := time.Parse(time.RFC3339Nano, values[0])
		if err != nil {
			return nil, nil, domain.ErrInvalidArgument
		}
		if requested.Before(end) {
			end = requested
		}
	}
	ctx, cancel := context.WithDeadline(ctx, end)
	return ctx, cancel, nil
}
func (h Handler) UUID(ctx context.Context, data []byte, headers nats.Header) []byte {
	id, input, err := jsonwire.DecodeUUID(data, "")
	if err != nil {
		return failure(err)
	}
	ctx, cancel, err := deadline(ctx, headers, h.Timeout)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	result, err := h.Service.UUID(ctx, id, input)
	if err != nil {
		return failure(err)
	}
	data, err = jsonwire.EncodeUUID(result)
	if err != nil {
		return failure(err)
	}
	return data
}
func (h Handler) X25519(ctx context.Context, data []byte, headers nats.Header) []byte {
	id, input, err := jsonwire.DecodeX25519(data, "")
	if err != nil {
		return failure(err)
	}
	ctx, cancel, err := deadline(ctx, headers, h.Timeout)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	result, err := h.Service.X25519(ctx, id, input)
	if err != nil {
		return failure(err)
	}
	data, err = jsonwire.EncodeCurve(result)
	if err != nil {
		return failure(err)
	}
	return data
}
func (h Handler) WireGuard(ctx context.Context, data []byte, headers nats.Header) []byte {
	id, input, err := jsonwire.DecodeWireGuard(data, "")
	if err != nil {
		return failure(err)
	}
	ctx, cancel, err := deadline(ctx, headers, h.Timeout)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	result, err := h.Service.WireGuard(ctx, id, input)
	if err != nil {
		return failure(err)
	}
	data, err = jsonwire.EncodeCurve(result)
	if err != nil {
		return failure(err)
	}
	return data
}
func (h Handler) MLDSA65(ctx context.Context, data []byte, headers nats.Header) []byte {
	id, input, err := jsonwire.DecodeSeed(data, "")
	if err != nil {
		return failure(err)
	}
	ctx, cancel, err := deadline(ctx, headers, h.Timeout)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	result, err := h.Service.MLDSA65(ctx, id, input)
	if err != nil {
		return failure(err)
	}
	data, err = jsonwire.EncodeMLDSA65(result)
	if err != nil {
		return failure(err)
	}
	return data
}
func (h Handler) MLKEM768(ctx context.Context, data []byte, headers nats.Header) []byte {
	id, input, err := jsonwire.DecodeSeed(data, "")
	if err != nil {
		return failure(err)
	}
	ctx, cancel, err := deadline(ctx, headers, h.Timeout)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	result, err := h.Service.MLKEM768(ctx, id, input)
	if err != nil {
		return failure(err)
	}
	data, err = jsonwire.EncodeMLKEM768(result)
	if err != nil {
		return failure(err)
	}
	return data
}
func (h Handler) VLESSEncryption(ctx context.Context, data []byte, headers nats.Header) []byte {
	id, err := jsonwire.DecodeEmpty(data, "")
	if err != nil {
		return failure(err)
	}
	ctx, cancel, err := deadline(ctx, headers, h.Timeout)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	result, err := h.Service.VLESSEncryption(ctx, id)
	if err != nil {
		return failure(err)
	}
	data, err = jsonwire.EncodeVLESS(result)
	if err != nil {
		return failure(err)
	}
	return data
}
func (h Handler) Certificate(ctx context.Context, data []byte, headers nats.Header) []byte {
	id, input, err := jsonwire.DecodeCertificate(data, "")
	if err != nil {
		return failure(err)
	}
	ctx, cancel, err := deadline(ctx, headers, h.Timeout)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	result, err := h.Service.Certificate(ctx, id, input)
	if err != nil {
		return failure(err)
	}
	data, err = jsonwire.EncodeCertificate(result)
	if err != nil {
		return failure(err)
	}
	return data
}
func (h Handler) CertificateHashes(ctx context.Context, data []byte, headers nats.Header) []byte {
	id, input, err := jsonwire.DecodeCertificateHashes(data, "")
	if err != nil {
		return failure(err)
	}
	ctx, cancel, err := deadline(ctx, headers, h.Timeout)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	result, err := h.Service.CertificateHashes(ctx, id, input)
	if err != nil {
		return failure(err)
	}
	data, err = jsonwire.EncodeCertificateHashes(result)
	if err != nil {
		return failure(err)
	}
	return data
}
func (h Handler) ECH(ctx context.Context, data []byte, headers nats.Header) []byte {
	id, input, err := jsonwire.DecodeECH(data, "")
	if err != nil {
		return failure(err)
	}
	ctx, cancel, err := deadline(ctx, headers, h.Timeout)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	result, err := h.Service.ECH(ctx, id, input)
	if err != nil {
		return failure(err)
	}
	data, err = jsonwire.EncodeECH(result)
	if err != nil {
		return failure(err)
	}
	return data
}
func failure(err error) []byte {
	data, _ := json.Marshal(struct {
		Error string `json:"error"`
	}{jsonwire.ErrorCode(err)})
	return data
}
