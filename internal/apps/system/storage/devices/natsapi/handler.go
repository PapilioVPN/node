// Package natsapi exposes bounded device I/O observations through Core NATS.
package natsapi

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/storage/devices/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/storage/devices/domain"
	"github.com/nats-io/nats.go"
)

type Handler struct {
	Service services.Service
	Timeout time.Duration
}

func (handler Handler) List(ctx context.Context, data []byte, headers nats.Header) []byte {
	fields, code := objectInput(data)
	if code != "" {
		return failure(code)
	}
	var selection domain.Selection
	for key, raw := range fields {
		switch key {
		case "after_name":
			var value *string
			if json.Unmarshal(raw, &value) != nil || value == nil {
				return failure("invalid_argument")
			}
			selection.AfterName = *value
		case "limit":
			var value *uint32
			if json.Unmarshal(raw, &value) != nil || value == nil {
				return failure("invalid_argument")
			}
			selection.Limit = *value
		default:
			return failure("invalid_argument")
		}
	}
	ctx, cancel, code := requestContext(ctx, headers, handler.Timeout)
	if code != "" {
		return failure(code)
	}
	defer cancel()
	result, err := handler.Service.List(ctx, selection)
	if err != nil {
		return readError(err)
	}
	encoded, err := json.Marshal(listResult(result))
	if err != nil {
		return failure("unavailable")
	}
	return encoded
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
	case errors.Is(err, domain.ErrResourceExhausted):
		code = "resource_exhausted"
	}
	return failure(code)
}
func failure(code string) []byte { return []byte(`{"error":"` + code + `"}`) }
