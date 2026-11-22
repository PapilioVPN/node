// Package natsapi exposes Xray configuration management to broker-authorized controllers.
package natsapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	"github.com/nats-io/nats.go"
)

type Handler struct {
	Service *services.Service
	State   *services.StateService
	Plans   *services.Plans
	Timeout time.Duration
}

func (h Handler) Validate(ctx context.Context, data []byte, headers nats.Header) []byte {
	ctx, cancel, err := h.requestContext(ctx, headers)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	id, candidate, err := decode(data)
	if err != nil {
		return failure(err)
	}
	result, err := h.Service.Validate(ctx, id, candidate)
	if err != nil {
		return failure(err)
	}
	return encode(struct {
		InstanceID string    `json:"instance_id"`
		Valid      bool      `json:"valid"`
		CheckedAt  time.Time `json:"checked_at"`
		DurationNS string    `json:"duration_ns"`
	}{result.InstanceID, result.Valid, result.CheckedAt, strconv.FormatInt(int64(result.Duration), 10)})
}

// Content uses standard JSON base64 for arbitrary native protobuf bytes too.
func decode(data []byte) (string, domain.Candidate, error) {
	if len(data) > 2<<20 {
		return "", domain.Candidate{}, domain.ErrTooLarge
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return "", domain.Candidate{}, domain.ErrInvalidArgument
	}
	var id string
	var candidate domain.Candidate
	seen := map[string]bool{}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return "", candidate, domain.ErrInvalidArgument
		}
		name, ok := key.(string)
		if !ok || seen[name] {
			return "", candidate, domain.ErrInvalidArgument
		}
		seen[name] = true
		switch name {
		case "instance_id":
			err = decoder.Decode(&id)
		case "format":
			err = decoder.Decode(&candidate.Format)
		case "content":
			err = decoder.Decode(&candidate.Content)
		default:
			return "", candidate, domain.ErrInvalidArgument
		}
		if err != nil {
			return "", candidate, domain.ErrInvalidArgument
		}
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') || len(seen) != 3 {
		return "", candidate, domain.ErrInvalidArgument
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return "", candidate, domain.ErrInvalidArgument
	}
	return id, candidate, nil
}

func errorCode(err error) string {
	code := "unavailable"
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		code = "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		code = "canceled"
	case errors.Is(err, domain.ErrInvalidArgument):
		code = "invalid_argument"
	case errors.Is(err, domain.ErrRejected):
		code = "native_rejected"
	case errors.Is(err, domain.ErrNotFound):
		code = "not_found"
	case errors.Is(err, domain.ErrPlanExpired):
		return "plan_expired"
	case errors.Is(err, domain.ErrStalePlan):
		return "stale_plan"
	case errors.Is(err, domain.ErrNoApplication):
		return "application_not_retained"
	case errors.Is(err, domain.ErrConflict):
		code = "revision_conflict"
	case errors.Is(err, domain.ErrDurabilityUnknown):
		code = "durability_unknown"
	case errors.Is(err, domain.ErrNoDesired), errors.Is(err, domain.ErrUnsupported), errors.Is(err, domain.ErrClosed):
		code = "failed_precondition"
	case errors.Is(err, domain.ErrTooLarge), errors.Is(err, domain.ErrCapacity):
		code = "resource_exhausted"
	}
	return code
}
func failure(err error) []byte {
	return encode(struct {
		Error string `json:"error"`
	}{errorCode(err)})
}

func encode(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		return []byte(`{"error":"unavailable"}`)
	}
	return data
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
