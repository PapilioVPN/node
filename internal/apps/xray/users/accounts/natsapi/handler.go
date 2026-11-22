// Package natsapi exposes account observations to broker-authorized controllers.
package natsapi

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/jsonwire"
	"github.com/nats-io/nats.go"
)

type Handler struct {
	Configurations *services.Configurations
	Service        *services.Service
	Sources        *services.Sources
	Timeout        time.Duration
}

func (h Handler) requestContext(ctx context.Context, headers nats.Header) (context.Context, context.CancelFunc, error) {
	deadline := time.Now().Add(h.Timeout)
	if values, ok := headers["Papilio-Deadline"]; ok {
		if len(values) != 1 {
			return nil, nil, domain.ErrInvalidArgument
		}
		requested, err := time.Parse(time.RFC3339Nano, values[0])
		if err != nil {
			return nil, nil, domain.ErrInvalidArgument
		}
		if requested.Before(deadline) {
			deadline = requested
		}
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	return ctx, cancel, nil
}
func (h Handler) Count(ctx context.Context, data []byte, headers nats.Header) []byte {
	input, err := jsonwire.DecodeSelection(data)
	if err != nil {
		return failure(err)
	}
	if input.Email != nil || input.HasPage() || input.AccountType != nil {
		return failure(domain.ErrInvalidArgument)
	}
	ctx, cancel, err := h.requestContext(ctx, headers)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	result, err := h.Service.Count(ctx, input.InstanceID, input.InboundTag)
	if err != nil {
		return failure(err)
	}
	encoded, err := jsonwire.EncodeCount(result)
	if err != nil {
		return failure(err)
	}
	return encoded
}
func (h Handler) Get(ctx context.Context, data []byte, headers nats.Header) []byte {
	input, err := jsonwire.DecodeSelection(data)
	if err != nil {
		return failure(err)
	}
	if input.Email == nil || input.HasPage() || input.AccountType != nil {
		return failure(domain.ErrInvalidArgument)
	}
	ctx, cancel, err := h.requestContext(ctx, headers)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	result, err := h.Service.Get(ctx, input.InstanceID, input.InboundTag, *input.Email)
	if err != nil {
		return failure(err)
	}
	encoded, err := jsonwire.EncodeAccount(result)
	if err != nil {
		return failure(err)
	}
	return encoded
}
func errorCode(err error) string {
	code := "unavailable"
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		code = "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		code = "canceled"
	case errors.Is(err, domain.ErrNotAttempted):
		code = "not_attempted"
	case errors.Is(err, domain.ErrStale):
		code = "stale_observation"
	case errors.Is(err, domain.ErrIncompatible):
		code = "incompatible_account"
	case errors.Is(err, domain.ErrInboundMissing):
		code = "inbound_not_found"
	case errors.Is(err, domain.ErrInboundAmbiguous):
		code = "ambiguous_inbound"
	case errors.Is(err, domain.ErrInvalidArgument):
		code = "invalid_argument"
	case errors.Is(err, domain.ErrNotFound):
		code = "not_found"
	case errors.Is(err, domain.ErrUnsupported), errors.Is(err, domain.ErrNotRunning):
		code = "failed_precondition"
	case errors.Is(err, domain.ErrUncertain):
		code = "restart_required"
	case errors.Is(err, domain.ErrExecutionChanged):
		code = "execution_changed"
	case errors.Is(err, domain.ErrCapacity):
		code = "resource_exhausted"
	}
	return code
}
func failure(err error) []byte {
	code := errorCode(err)
	data, _ := json.Marshal(struct {
		Error string `json:"error"`
	}{code})
	return data
}
