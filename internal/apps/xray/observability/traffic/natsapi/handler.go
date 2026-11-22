// Package natsapi exposes exact traffic reads to broker-authorized controllers.
package natsapi

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/infra/jsonwire"
	"github.com/nats-io/nats.go"
)

type Handler struct {
	Service *services.Service
	Timeout time.Duration
}

func (h Handler) Read(ctx context.Context, data []byte, headers nats.Header) []byte {
	id, selection, err := jsonwire.DecodeRequest(data)
	if err != nil {
		return failure(err)
	}
	ctx, cancel, err := h.deadline(ctx, headers)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	result, err := h.Service.Read(ctx, id, selection)
	if err != nil {
		return failure(err)
	}
	encoded, err := jsonwire.Encode(result)
	if err != nil {
		return failure(err)
	}
	return encoded
}

func failure(err error) []byte {
	code := "unavailable"
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		code = "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		code = "canceled"
	case errors.Is(err, domain.ErrInvalidArgument):
		code = "invalid_argument"
	case errors.Is(err, domain.ErrConflict):
		code = "revision_conflict"
	case errors.Is(err, domain.ErrContinuation):
		code = "continuation_expired"
	case errors.Is(err, domain.ErrRestartRequired):
		code = "restart_required"
	case errors.Is(err, domain.ErrExecutionChanged):
		code = "execution_changed"
	case errors.Is(err, domain.ErrCounterNotFound):
		code = "counter_not_found"
	case errors.Is(err, domain.ErrNotFound):
		code = "not_found"
	case errors.Is(err, domain.ErrUnsupported), errors.Is(err, domain.ErrClosed), errors.Is(err, domain.ErrNotRunning):
		code = "failed_precondition"
	case errors.Is(err, domain.ErrCapacity):
		code = "resource_exhausted"
	}
	data, _ := json.Marshal(struct {
		Error string `json:"error"`
	}{code})
	return data
}
