package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	"github.com/PapilioVPN/node/internal/engines/xray/outbounds/health"
	"github.com/go-chi/chi/v5"
)

type observation struct {
	Tag                 string        `json:"tag"`
	Status              health.Status `json:"status"`
	DelayMilliseconds   *int64        `json:"delay_milliseconds,omitempty"`
	LastSeenUnixSeconds *int64        `json:"last_seen_unix_seconds,omitempty"`
	LastTryUnixSeconds  *int64        `json:"last_try_unix_seconds,omitempty"`
}

func Register(router chi.Router, service *health.Service) {
	router.Get("/v1/xray/instances/{id}/outbounds/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		values, err := url.ParseQuery(r.URL.RawQuery)
		tags := values["tag"]
		if err != nil || len(tags) > 1 {
			writeError(w, health.ErrInvalid)
			return
		}
		request := health.Request{InstanceID: chi.URLParam(r, "id")}
		if len(tags) == 1 {
			request.Tag = &tags[0]
		}
		samples, err := service.Get(r.Context(), request)
		if err != nil {
			writeError(w, err)
			return
		}
		result := struct {
			Observations []observation `json:"observations"`
		}{Observations: make([]observation, len(samples))}
		for i, sample := range samples {
			result.Observations[i] = observation(sample)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	})
}

func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, health.ErrInvalid):
		http.Error(w, health.ErrInvalid.Error(), http.StatusBadRequest)
	case errors.Is(err, health.ErrNotFound):
		http.Error(w, health.ErrNotFound.Error(), http.StatusNotFound)
	case errors.Is(err, health.ErrDisabled):
		http.Error(w, health.ErrDisabled.Error(), http.StatusConflict)
	case errors.Is(err, health.ErrBusy):
		http.Error(w, health.ErrBusy.Error(), http.StatusTooManyRequests)
	case errors.Is(err, health.ErrTooLarge):
		http.Error(w, health.ErrTooLarge.Error(), http.StatusUnprocessableEntity)
	case errors.Is(err, context.Canceled):
		http.Error(w, "Xray outbound health request canceled", http.StatusRequestTimeout)
	case errors.Is(err, context.DeadlineExceeded):
		http.Error(w, "Xray outbound health request deadline exceeded", http.StatusGatewayTimeout)
	default:
		http.Error(w, health.ErrUnavailable.Error(), http.StatusServiceUnavailable)
	}
}
