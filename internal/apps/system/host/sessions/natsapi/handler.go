// Package natsapi exposes bounded session observations through Core NATS.
package natsapi

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/sessions/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/host/sessions/domain"
	"github.com/nats-io/nats.go"
)

type Handler struct {
	Service services.Service
	Timeout time.Duration
}

func (handler Handler) List(ctx context.Context, data []byte, headers nats.Header) []byte {
	input, code := selectionInput(data)
	if code != "" {
		return failure(code)
	}
	ctx, cancel, code := requestContext(ctx, headers, handler.Timeout)
	if code != "" {
		return failure(code)
	}
	defer cancel()
	result, err := handler.Service.List(ctx, input)
	if err != nil {
		return readError(err)
	}
	encoded, err := json.Marshal(listResult(result))
	if err != nil {
		return failure("unavailable")
	}
	return encoded
}
func (handler Handler) ListRecords(ctx context.Context, data []byte, headers nats.Header) []byte {
	input, code := selectionInput(data)
	if code != "" {
		return failure(code)
	}
	ctx, cancel, code := requestContext(ctx, headers, handler.Timeout)
	if code != "" {
		return failure(code)
	}
	defer cancel()
	result, err := handler.Service.ListRecords(ctx, input)
	if err != nil {
		return readError(err)
	}
	encoded, err := json.Marshal(recordsResult(result))
	if err != nil {
		return failure("unavailable")
	}
	return encoded
}
func (handler Handler) Read(ctx context.Context, data []byte, headers nats.Header) []byte {
	fields, code := objectInput(data)
	if code != "" {
		return failure(code)
	}
	var id *string
	if len(fields) != 1 || json.Unmarshal(fields["id"], &id) != nil || id == nil {
		return failure("invalid_argument")
	}
	ctx, cancel, code := requestContext(ctx, headers, handler.Timeout)
	if code != "" {
		return failure(code)
	}
	defer cancel()
	result, err := handler.Service.Read(ctx, *id)
	if err != nil {
		return readError(err)
	}
	encoded, err := json.Marshal(readResult(result))
	if err != nil {
		return failure("unavailable")
	}
	return encoded
}
func selectionInput(data []byte) (domain.Selection, string) {
	fields, code := objectInput(data)
	if code != "" {
		return domain.Selection{}, code
	}
	var input domain.Selection
	for key, raw := range fields {
		var value *uint32
		if json.Unmarshal(raw, &value) != nil || value == nil {
			return input, "invalid_argument"
		}
		switch key {
		case "offset":
			input.Offset = *value
		case "limit":
			input.Limit = *value
		default:
			return input, "invalid_argument"
		}
	}
	return input, ""
}

func objectInput(data []byte) (map[string]json.RawMessage, string) {
	if len(data) > 2048 {
		return nil, "resource_exhausted"
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return nil, "invalid_argument"
	}
	return fields, ""
}
func readError(err error) []byte {
	code := "unavailable"
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		code = "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		code = "canceled"
	case errors.Is(err, domain.ErrInvalidArgument):
		code = "invalid_argument"
	case errors.Is(err, domain.ErrNotFound):
		code = "not_found"
	case errors.Is(err, domain.ErrPermissionDenied):
		code = "permission_denied"
	case errors.Is(err, domain.ErrUnsupported):
		code = "unimplemented"
	case errors.Is(err, domain.ErrResourceExhausted):
		code = "resource_exhausted"
	}
	return failure(code)
}
func failure(code string) []byte { return []byte(`{"error":"` + code + `"}`) }
