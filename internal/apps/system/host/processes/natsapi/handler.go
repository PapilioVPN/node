// Package natsapi exposes bounded process observations through Core NATS.
package natsapi

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/processes/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/host/processes/domain"
	"github.com/nats-io/nats.go"
)

type Handler struct {
	Service services.Service
	Timeout time.Duration
}

func (handler Handler) Count(ctx context.Context, data []byte, headers nats.Header) []byte {
	fields, code := objectInput(data)
	if code != "" {
		return failure(code)
	}
	if len(fields) != 0 {
		return failure("invalid_argument")
	}
	ctx, cancel, code := requestContext(ctx, headers, handler.Timeout)
	if code != "" {
		return failure(code)
	}
	defer cancel()
	result, err := handler.Service.Count(ctx)
	if err != nil {
		return readError(err)
	}
	encoded, err := json.Marshal(countResult(result))
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
	var pid *int32
	if len(fields) != 1 || json.Unmarshal(fields["pid"], &pid) != nil || pid == nil {
		return failure("invalid_argument")
	}
	ctx, cancel, code := requestContext(ctx, headers, handler.Timeout)
	if code != "" {
		return failure(code)
	}
	defer cancel()
	result, err := handler.Service.Read(ctx, *pid)
	if err != nil {
		return readError(err)
	}
	encoded, err := json.Marshal(readResult(result))
	if err != nil {
		return failure("unavailable")
	}
	return encoded
}
func (handler Handler) List(ctx context.Context, data []byte, headers nats.Header) []byte {
	fields, code := objectInput(data)
	if code != "" {
		return failure(code)
	}
	var selection domain.Selection
	for key, raw := range fields {
		switch key {
		case "after_pid":
			var value *int32
			if json.Unmarshal(raw, &value) != nil || value == nil {
				return failure("invalid_argument")
			}
			selection.AfterPID = *value
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
	if len(data) > 1024 {
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
	case errors.Is(err, domain.ErrResourceExhausted):
		code = "resource_exhausted"
	}
	return failure(code)
}
func failure(code string) []byte { return []byte(`{"error":"` + code + `"}`) }
