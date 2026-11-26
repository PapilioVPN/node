// Package httpapi provides Hysteria-specific HTTP boundary helpers.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/PapilioVPN/node/internal/engines/hysteria/internal/fault"
	"github.com/PapilioVPN/node/internal/engines/hysteria/logs/live"
	"github.com/PapilioVPN/node/internal/engines/hysteria/stats/observations"
	"github.com/go-chi/chi/v5"
)

const InstancePath = "/v1/hysteria/instances/{id}"

func ID(r *http.Request) string { return chi.URLParam(r, "id") }

func ReadBody(w http.ResponseWriter, r *http.Request, max int64) ([]byte, error) {
	if r.ContentLength == 0 {
		return nil, nil
	}
	if r.ContentLength > max {
		return nil, fault.ErrTooLarge
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, max))
	switch {
	case err == nil:
		return data, nil
	case r.Context().Err() != nil:
		return nil, r.Context().Err()
	default:
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			return nil, fault.ErrTooLarge
		}
		return nil, fault.ErrInvalid
	}
}

func WriteJSON(w http.ResponseWriter, code int, result any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(result)
}

func WriteError(w http.ResponseWriter, err error) {
	code, message := ErrorStatus(err)
	w.Header().Set("Cache-Control", "no-store")
	http.Error(w, message, code)
}

func ErrorStatus(err error) (int, string) {
	switch {
	case errors.Is(err, fault.ErrNotFound), errors.Is(err, observations.ErrNotFound):
		return http.StatusNotFound, fault.ErrNotFound.Error()
	case errors.Is(err, fault.ErrInvalid), errors.Is(err, observations.ErrInvalid):
		return http.StatusBadRequest, fault.ErrInvalid.Error()
	case errors.Is(err, fault.ErrTooLarge):
		return http.StatusRequestEntityTooLarge, fault.ErrTooLarge.Error()
	case errors.Is(err, fault.ErrBusy):
		return http.StatusConflict, fault.ErrBusy.Error()
	case errors.Is(err, fault.ErrInactive), errors.Is(err, observations.ErrInactive), errors.Is(err, live.ErrInactive):
		return http.StatusConflict, fault.ErrInactive.Error()
	case errors.Is(err, observations.ErrCapacity), errors.Is(err, live.ErrCapacity):
		return http.StatusTooManyRequests, "Hysteria operation capacity reached"
	case errors.Is(err, observations.ErrTooLarge):
		return http.StatusUnprocessableEntity, observations.ErrTooLarge.Error()
	case errors.Is(err, observations.ErrEffectUnknown):
		return http.StatusServiceUnavailable, observations.ErrEffectUnknown.Error()
	case errors.Is(err, context.Canceled):
		return http.StatusRequestTimeout, "Hysteria operation canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout, "Hysteria operation deadline exceeded"
	default:
		return http.StatusServiceUnavailable, fault.ErrUnavailable.Error()
	}
}
