package httpapi

import (
	"context"
	"encoding/json"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/infra/jsonwire"
	"github.com/gofiber/fiber/v3"
)

// queryEnvelope adapts numeric query strings without accepting duplicates or an
// unexpected field. jsonwire retains the one strict operation shape boundary.
func queryEnvelope(r fiber.Ctx) ([]byte, error) {
	if len(r.Body()) != 0 || len(r.Request().URI().QueryString()) > jsonwire.MaxRequestBytes {
		return nil, domain.ErrInvalidArgument
	}
	fields := map[string]json.RawMessage{}
	valid := true
	id, _ := json.Marshal(r.Params("id"))
	fields["instance_id"] = id
	r.Request().URI().QueryArgs().VisitAll(func(k, v []byte) {
		key := string(k)
		if _, ok := fields[key]; ok {
			valid = false
		}
		switch key {
		case "page_size", "offset":
			fields[key] = append([]byte{}, v...)
		default:
			fields[key], _ = json.Marshal(string(v))
		}
	})
	if !valid {
		return nil, domain.ErrInvalidArgument
	}
	data, err := json.Marshal(fields)
	if err != nil {
		return nil, domain.ErrInvalidArgument
	}
	return data, nil
}
func send(r fiber.Ctx, data []byte, err error) error {
	if err != nil {
		return failure(r, err)
	}
	r.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	return r.Send(data)
}
func (h Handler) List(r fiber.Ctx) error {
	r.Set(fiber.HeaderCacheControl, "no-store")
	data, err := queryEnvelope(r)
	if err != nil {
		return failure(r, err)
	}
	id, request, err := jsonwire.DecodePage(data)
	if err != nil {
		return failure(r, err)
	}
	ctx, cancel := context.WithTimeout(r.Context(), h.Timeout)
	defer cancel()
	result, err := h.Service.List(ctx, id, request)
	if err != nil {
		return failure(r, err)
	}
	data, err = jsonwire.EncodePage(result)
	return send(r, data, err)
}
func (h Handler) Pair(r fiber.Ctx) error {
	r.Set(fiber.HeaderCacheControl, "no-store")
	data, err := queryEnvelope(r)
	if err != nil {
		return failure(r, err)
	}
	id, scope, name, err := jsonwire.DecodePair(data)
	if err != nil {
		return failure(r, err)
	}
	ctx, cancel := context.WithTimeout(r.Context(), h.Timeout)
	defer cancel()
	result, err := h.Service.ReadPair(ctx, id, scope, name)
	if err != nil {
		return failure(r, err)
	}
	data, err = jsonwire.EncodePair(result)
	return send(r, data, err)
}
func (h Handler) ResetCounter(r fiber.Ctx) error { return h.reset(r, domain.ResetExact) }
func (h Handler) ResetPair(r fiber.Ctx) error    { return h.reset(r, domain.ResetPair) }
func (h Handler) ResetScope(r fiber.Ctx) error   { return h.reset(r, domain.ResetScope) }
func (h Handler) reset(r fiber.Ctx, kind domain.ResetKind) error {
	r.Set(fiber.HeaderCacheControl, "no-store")
	if len(r.Request().URI().QueryString()) != 0 || r.Get(fiber.HeaderContentType) != fiber.MIMEApplicationJSON {
		return failure(r, domain.ErrInvalidArgument)
	}
	// Instance identity belongs to the URL; reject a second body identity.
	fields, err := jsonwire.Fields(r.Body(), "epoch", "operation_id", "expected_revision", "scope", "name", "direction", "max_counters")
	if err != nil {
		return failure(r, err)
	}
	fields["instance_id"], _ = json.Marshal(r.Params("id"))
	data, err := json.Marshal(fields)
	if err != nil {
		return failure(r, err)
	}
	id, request, err := jsonwire.DecodeReset(data, kind)
	if err != nil {
		return failure(r, err)
	}
	ctx, cancel := context.WithTimeout(r.Context(), h.Timeout)
	defer cancel()
	result, err := h.Service.Reset(ctx, id, request)
	if err != nil {
		return failure(r, err)
	}
	data, err = jsonwire.EncodeReset(result)
	return send(r, data, err)
}
func (h Handler) ResetStatus(r fiber.Ctx) error {
	r.Set(fiber.HeaderCacheControl, "no-store")
	data, err := queryEnvelope(r)
	if err != nil {
		return failure(r, err)
	}
	id, revision, offset, err := jsonwire.DecodeStatus(data)
	if err != nil {
		return failure(r, err)
	}
	result, err := h.Service.ResetStatus(r.Context(), id, revision, offset)
	if err != nil {
		return failure(r, err)
	}
	data, err = jsonwire.EncodeReset(result)
	return send(r, data, err)
}
