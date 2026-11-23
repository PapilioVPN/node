package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	"github.com/PapilioVPN/node/internal/engines/xray/instances/lifecycle"
	"github.com/PapilioVPN/node/internal/engines/xray/stats/traffic"
	"github.com/go-chi/chi/v5"
)

func resetTraffic(service *traffic.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		values, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil || len(values["name"]) != 1 || len(values["direction"]) != 1 || (r.Body != nil && r.Body != http.NoBody) {
			writeResetError(w, traffic.ErrInvalid)
			return
		}
		result, err := service.Reset(r.Context(), traffic.Counter{
			ID: chi.URLParam(r, "id"), Scope: traffic.Scope(chi.URLParam(r, "scope")),
			Name: values.Get("name"), Direction: traffic.Direction(values.Get("direction")),
		})
		if err != nil {
			writeResetError(w, err)
			return
		}
		_ = json.NewEncoder(w).Encode(struct {
			PreviousBytes int64 `json:"previous_bytes"`
		}{result})
	}
}

func resetTrafficPair(service *traffic.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		values, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil || len(values["name"]) != 1 || values.Has("direction") || (r.Body != nil && r.Body != http.NoBody) {
			writeResetError(w, traffic.ErrInvalid)
			return
		}
		result, err := service.ResetPair(r.Context(), traffic.CounterPair{
			ID: chi.URLParam(r, "id"), Scope: traffic.Scope(chi.URLParam(r, "scope")), Name: values.Get("name"),
		})
		if err != nil {
			if result.Uplink.Effect == "" {
				writeResetError(w, err)
				return
			}
			code := http.StatusConflict
			if errors.Is(err, traffic.ErrResetUnknown) {
				code = http.StatusBadGateway
			}
			w.WriteHeader(code)
		}
		_ = json.NewEncoder(w).Encode(struct {
			Uplink   directionReset `json:"uplink"`
			Downlink directionReset `json:"downlink"`
		}{directionResponse(&result.Uplink), directionResponse(&result.Downlink)})
	}
}

type directionReset struct {
	Effect        traffic.ResetEffect `json:"effect"`
	PreviousBytes *int64              `json:"previous_bytes,omitempty"`
}

func directionResponse(result *traffic.DirectionReset) directionReset {
	reply := directionReset{Effect: result.Effect}
	if result.Effect == traffic.ResetAcknowledged {
		reply.PreviousBytes = &result.PreviousBytes
	}
	return reply
}

func writeResetError(w http.ResponseWriter, err error) {
	code, effect, message := http.StatusServiceUnavailable, "not_applied", traffic.ErrUnavailable.Error()
	switch {
	case errors.Is(err, traffic.ErrResetUnknown):
		code, effect, message = http.StatusBadGateway, "unknown", traffic.ErrResetUnknown.Error()
	case errors.Is(err, traffic.ErrResetDisabled):
		code, message = http.StatusForbidden, traffic.ErrResetDisabled.Error()
	case errors.Is(err, lifecycle.ErrNotFound):
		code, message = http.StatusNotFound, lifecycle.ErrNotFound.Error()
	case errors.Is(err, traffic.ErrNotFound):
		code, message = http.StatusNotFound, traffic.ErrNotFound.Error()
	case errors.Is(err, traffic.ErrInvalid):
		code, message = http.StatusBadRequest, traffic.ErrInvalid.Error()
	case errors.Is(err, traffic.ErrTooLarge):
		code, message = http.StatusUnprocessableEntity, traffic.ErrTooLarge.Error()
	case errors.Is(err, traffic.ErrResetBusy):
		code, message = http.StatusTooManyRequests, traffic.ErrResetBusy.Error()
	case errors.Is(err, context.DeadlineExceeded):
		code, message = http.StatusGatewayTimeout, context.DeadlineExceeded.Error()
	case errors.Is(err, context.Canceled):
		code, message = http.StatusRequestTimeout, context.Canceled.Error()
	}
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(struct {
		Effect  string `json:"effect"`
		Message string `json:"message"`
	}{effect, message})
}
